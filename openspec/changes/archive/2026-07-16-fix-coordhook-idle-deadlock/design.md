## Context

Confirmed via a live incident, not a hypothetical: a coordinator crossed its configured `coordinator_context_budget`, the Stop hook (`cmd/argus/coord_hook.go`'s `runCoordHook`) emitted its "reach a safe seam and recycle" block decision, the coordinator complied and called `hera_status(request_recycle=true)` (which sets `task_meta(hera, pending_recycle) = "true"`, per `internal/mcp/hera.go`), and then the hook fired again on the very next Stop event — still over budget, with zero memory that recycle had already been requested — and blocked again. A Stop-hook "block" decision forces Claude Code to keep going immediately; the session's PTY never gets 3 consecutive idle seconds, which is what `internal/agent/session.go`'s `IsIdle` (`idleThreshold = 3 * time.Second`) requires before `internal/hera/recycle_watcher.go`'s `RecycleWatcher` will actually drive `RecycleCoord` through its kill-and-restart. The loop ran 15+ times, with `context_size` climbing from ~221K to ~267K tokens, before a human manually disabled the hook to break it.

Root cause: `runCoordHook` is a pure function of its inputs (`ARGUS_TASK_ID`, the transcript, the daemon's REST state) with no notion of "have I already told this coordinator to recycle." Every invocation is a fresh process — there's genuinely nowhere to keep in-memory state between Stop events — so the only place to check "already requested" is the same `task_meta` row `hera_status(request_recycle=true)` already writes.

## Goals / Non-Goals

**Goals:**

- Stop the Stop hook from re-blocking a coordinator that has already requested a self-service recycle — the idempotency fix (Part A).
- Add a hard-stop escalation that fires unconditionally once `context_size` crosses 1.5x budget, forcing an immediate recycle rather than waiting on a graceful path that may never see idleness (Part B) — the safety net for exactly the failure mode above, in case a human keeps the coordinator busy enough that it never idles even with Part A fixed.
- Reuse the exact same kill-and-restart mechanism the rail's `B` key already exercises (`heraDoForceRecycle` / `hera.RecycleCoord` with `RecycleHumanForced`), just reachable from the coord-hook CLI process over the daemon's existing Unix socket.

**Non-Goals:**

- Changing `RecycleWatcher`'s idle-polling cadence or `IsIdle`'s 3s threshold — those are correct as specced; the bug is the hook re-blocking before idleness can ever be observed, not the idle detection itself.
- Making the Stop hook stateful across processes (e.g. a local sentinel file) — `task_meta(hera, pending_recycle)` is already the durable, daemon-visible signal for "recycle requested"; reusing it avoids inventing a second source of truth.
- Changing `hera_status(request_recycle=true)`'s write path or `RecycleCoord`'s self-service semantics — both are correct as specced; this change only fixes the hook's re-evaluation logic and adds the new hard-stop escalation on top.
- A configurable hard-stop multiplier — 1.5x is a reasonable fixed constant for a safety net that should rarely fire; no config surface is added for it.

## Decisions

**Decision: check `pending_recycle` via a second REST round trip (`pendingRecycleAlreadyReal`), not folded into the existing `resolveRoleKindReal` call.**

Alternatives considered:
1. *Extend `resolveRoleKindReal`'s single response parse to also return the pending-recycle flag, avoiding a second GET.* Rejected for this change: it would require changing `ResolveRoleKind`'s return signature (breaking every existing call site and test), and role-kind gating happens unconditionally on every Stop event while pending-recycle only needs to be read once a coordinator is already confirmed over budget — coupling the two would mean parsing/threading a value that's usually unused.
2. **(Chosen) A separate `PendingRecycleAlready func(taskID string) (bool, error)` field on `coordHookEnv`, with its own `pendingRecycleAlreadyReal` hitting the same `GET /api/tasks/{id}/meta?namespace=hera` endpoint.** Costs one extra HTTP round trip per over-budget Stop event (not per Stop event overall — under-budget turns never reach this check) against a loopback daemon; simplicity and call-site independence outweigh that cost for a Stop hook that already pays one round trip per turn for role resolution.

**Decision: the hard-stop escalation (`ForceRecycleCoordinator`) is a plain daemon-registered RPC method (like `Ports`/`ClipboardSet`), not a session-supervisor protocol verb.**

Rationale: `ProtocolVersion` (`internal/daemon/types.go`) versions the R/S handshake between the daemon and the session-supervisor — it exists so a daemon can feature-detect an older, already-running supervisor's capabilities without restarting it (restarting would SIGHUP its agents). `ForceRecycleCoordinator` doesn't touch that protocol at all: it's daemon-side logic (`s.daemon.db`, `s.runner`, `s.cfgFn` — the same three things `HeraRecycleRunner` already closes over) exposed the same way `KBSearch`/`ClipboardSet`/`Ports` are — registered automatically via `server.RegisterName("Daemon", svc)`'s reflection over `RPCService`'s exported methods. No `ProtocolVersion` bump is needed because this method is never proxied to or served by the supervisor.

**Decision: the hard-stop threshold check happens BEFORE the `pending_recycle` check, and skips reading `pending_recycle` entirely when it fires.**

Rationale: the escalation is meant to fire "regardless of whether pending_recycle is already set" (it's a safety net for exactly the case where the graceful path is stuck), so there's no scenario where reading the flag would change the outcome — skipping the read saves a REST round trip on the hot (once-over-threshold) path.

**Decision: integer-safe 1.5x comparison via `size*2 >= budget*3`, not a float division.**

Rationale: avoids float rounding/precision concerns entirely for a threshold comparison over token counts that are always non-negative integers; `size*2 >= budget*3` is exactly equivalent to `size >= budget * 1.5` with no floating point involved.

## Risks / Trade-offs

- **Risk:** A coordinator could set `pending_recycle=true` and then never actually go idle (e.g. because a human keeps it busy), leaving it stuck at whatever `context_size` it was at when it requested recycle, un-nudged, forever. → **Mitigation:** this is exactly what the hard-stop escalation (Part B) exists for — once `context_size` crosses 1.5x budget, the graceful/idle-gated path is bypassed entirely and a recycle is forced immediately.
- **Risk:** `ForceRecycleCoordinator` reached from a coord-hook process that's confused about which task it's running in (e.g. a stale `ARGUS_TASK_ID`) could recycle the wrong coordinator. → **Mitigation:** identical risk already exists for the rail's `B` key and for every other coord-hook REST call, all of which trust `ARGUS_TASK_ID`; not a new attack surface introduced by this change.
- **Trade-off:** the pending-recycle check adds one more REST round trip to the Stop hook's over-budget path. → Accepted: Stop hooks already pay a per-turn round trip for role resolution and budget lookup; one more only on already-over-budget turns is not the dominant cost.

## Migration Plan

- Code change ships as a normal PR; no schema migration (reuses the existing `task_meta(hera, pending_recycle)` key, already written by `hera_status`).
- No `ProtocolVersion` bump (see Decisions above) — the new RPC verb is not part of the session-supervisor handshake protocol.
- Rollback: revert the PR; the hook returns to its pre-fix (buggy but not newly-broken) behavior.

## Open Questions

None — the bug was confirmed via a live incident (not theorized), and both fixes were scoped directly against the reproduced failure mode.

## Acceptance criteria

- It should NOT emit a block decision when a coordinator's `context_size` is at/over budget and `task_meta(hera, pending_recycle)` is already `"true"` — the turn is allowed to genuinely end.
- It should still emit the graceful block decision on the FIRST over-budget turn (before `pending_recycle` has been set), unchanged from today's behavior.
- It should fall back to emitting the graceful block decision (not silently no-op) if reading `pending_recycle` itself errors, so a transient read failure can't swallow the nudge.
- It should call `Daemon.ForceRecycleCoordinator` once `context_size >= budget * 1.5` (integer-safe: `size*2 >= budget*3`), and must NOT also emit the graceful block decision on that same turn.
- It should call `Daemon.ForceRecycleCoordinator` at the hard-stop threshold regardless of whether `pending_recycle` is already `"true"` — the escalation is unconditional.
- `Daemon.ForceRecycleCoordinator` should resolve the coordinator-kind role bound to the given task ID and force-recycle it via `hera.RecycleCoord(..., RecycleHumanForced)`, identically to the rail's `B` key.
- `Daemon.ForceRecycleCoordinator` should return an error (not panic, not silently no-op) for a task ID with no bound coordinator role — whether unbound entirely or bound only to a worker/freelance role.
