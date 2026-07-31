## Context

The TUI's native Hera view already has a manual revive path, fired by `Enter` on a rail row (`internal/tui/hera/page.go`'s `OnReattach`, wired to `App.heraReattach` in `internal/tui/heraactions.go`):

- No live session (`sess == nil || !sess.Alive()`) → `a.startSession(t)` restarts the session, resuming via `--session-id` when the task carries one. Fires for any role kind, including a dead coordinator.
- Live session, role kind `coordinator` → navigate-only. A live coordinator is presumed operator-interactive (a human may be reading its pane right now); `Enter` never auto-restarts it.
- Live session, role kind `worker`/`freelance` → `a.reviveHeraWorker(task, sess)`: off the main goroutine, checks `sess.IsIdle()` and `sessionBlockedOnPrompt` (a SIGTSTP'd or otherwise stalled agent is idle and NOT parked at a prompt — that's the "genuinely stuck" signature); if both hold, and no kick is already in flight (`HasPendingRestart`), it calls `agent.SessionRunner.KickRerender` (stop + resume in place at the pane's current width) and, on success, restores the task from `in_review` back to `in_progress` via `db.ReviveHeraWorkerToInProgress` — already documented as "the SINGLE shared helper behind BOTH revive triggers" (the other being the daemon's supervisor-mode startup reattach).

This is a keystroke a human drives from the TUI. A hera coordinator is itself just another agent session — it cannot press `Enter`. It has no way today to notice a bound role has gone quiet (dead or SIGTSTP'd after a session-supervisor restart) and do anything about it, short of spawning a wasteful duplicate worker.

## Goals / Non-Goals

**Goals:**

- Give a coordinator an MCP tool that inspects ONE role it coordinates and, if dead or genuinely stuck, revives it — using the identical safety gate the TUI's `Enter` key already enforces, so it cannot thrash a session that is actually working or waiting on a user prompt.
- Report a no-op clearly (busy / blocked / live coordinator / already mid-restart) rather than silently doing nothing.
- Keep this strictly pull/on-demand: the daemon never fires this automatically on supervisor restart. A coordinator calls it when it notices no progress (`hera_status`, `hera_tree_updates`).

**Non-Goals:**

- No automatic revive-on-supervisor-restart. That would be a push model; this change is deliberately pull-only (proposal.md).
- No change to the TUI's `Enter`-key behavior or its threading model.
- No new "needs-input detection" heuristic — reuses `agent.BlockedOnPrompt`/`agent.DetectNeedsInput` exactly as they exist today.

## Decisions

### D1 — Tool shape: `hera_revive(cwd, role_name, [orchestrator])`, coordinator-only

Mirrors `hera_block`'s addressing (`resolveOrchRole`: a role name resolved within the caller's own orchestrator) and `hera_spawn_worker`'s coordinator-only guard. A worker/freelance caller is rejected with the same wording style as `hera_spawn_worker`/the plan-authoring tools ("only coordinators may ..."). Targeting the caller's own (necessarily-live, since it's calling right now) role is rejected explicitly with a clearer message than letting it fall through to "skipped: live coordinator."

Alternative considered: let any hera-bound role revive any other role process-wide (no orchestrator scoping). Rejected — every other hera mutation tool (`hera_block`, `hera_plan_node`, `hera_send`'s `to`) scopes role addressing to the caller's own orchestrator; there's no reason to widen the blast radius here.

### D2 — Outcome is always reported, never silently a no-op

`hera_revive` returns one of: `restarted_dead`, `kicked_stuck`, `skipped_coordinator_live`, `skipped_busy`, `skipped_blocked_on_prompt`, `skipped_restart_pending`, `skipped_no_session_id`. This directly satisfies "if the role is fine, the tool should say so rather than doing anything" (task brief) — a coordinator gets a legible signal either way instead of having to infer success from a follow-up `hera_status` poll.

### D3 — Extract the decision primitive; do NOT extract the TUI's call site (the central question this change was asked to settle)

**What gets extracted:** a new pure function, `internal/hera.ReviveRole(store, runner, taskID, isCoordinator) (ReviveOutcome, error)`, over two narrow interfaces (`ReviveStore`, `ReviveRunner`) — architecturally identical to the existing `hera.RecycleCoord`/`RecycleStore`/`RecycleRunner` (`internal/hera/recycle.go`), which is this codebase's own precedent for "one gating+action function, called from both a TUI action and a daemon-side trigger." `ReviveRole` encodes the exact same ordered gate the TUI applies (alive? → coordinator-live? → has a session id? → kick already in flight? → idle? → blocked on prompt?) and the same two actions (`RestartDead` / `KickRerender` + best-effort `ReviveHeraWorkerToInProgress` restore). It is unit-testable with fakes, no real PTY or SQLite required (`internal/hera/revive_test.go`, mirroring `recycle_test.go`'s shape).

A new daemon-side adapter, `daemon.HeraReviveRunner` (mirrors `daemon.HeraRecycleRunner` in `internal/daemon/recycle.go`), implements `ReviveRunner` against the real `*db.DB` + `agent.SessionRunner`, and is the ONLY thing `hera_revive`'s MCP handler wires up.

**What does NOT get touched:** `internal/tui/heraactions.go`'s `heraReattach`/`reviveHeraWorker`/`reviveRestoreInProgress`. They keep their existing inline implementation. Three concrete reasons, not just "avoid churn":

1. **A genuine behavioral difference, not an accidental one.** The TUI's kick resizes to `a.computePTYSize()` — the CURRENT PANE's dimensions, so a revived session also re-flows to whatever size the pane is now (this doubles as the BUG-074 size-drift fix path). A headless MCP caller has no pane and no rendering surface to fit; the correct default there is to PRESERVE the session's existing PTY size (`sess.PTYSize()`), which is what `daemon.HeraReviveRunner.KickRerender` does. Forcing one shared call site would mean either the daemon path grows a fake "target size" concept it has no use for, or the TUI path silently loses its resize-on-revive side effect.
2. **Threading.** The TUI's version deliberately splits "compute idle/blocked off the main goroutine" from "act inside `QueueUpdateDraw`" (mirroring `maybeKickRerenderAtWidth`) — a pattern this codebase has hard-won, well-tested threading rules around (`gotchas/ui-threading.md`). The MCP handler runs on its own request goroutine with no such constraint. Collapsing the TUI's two-phase dispatch into one straight-line call (as `ReviveRole` is) is a reasonable simplification in isolation, but changing it is exactly the kind of "refactor beyond what the task requires" this repo's conventions call out to avoid, against a code path with a long specific bug history (BUG-032/033/034/035/060/061/063/065/067/072 in `gotchas/events.md`, all in this exact idle/needs-input detection neighborhood).
3. **Scope.** The task asked for a coordinator-facing pull-revive tool, not a TUI refactor. The TUI path is provided as background/context, not as something in scope to change.

**Why this isn't "silent duplication" despite the TUI keeping its own inline branch:** every individual CHECK the TUI's inline code performs is already, and remains, single-sourced regardless of this decision — `agent.BlockedOnPrompt` (already documented as "correct server-side... unreliable in daemon-client mode," i.e., already written with exactly this daemon-vs-TUI split in mind), `db.ReviveHeraWorkerToInProgress` (already documented as "the SINGLE shared helper" — this change adds a THIRD caller, not a fork), and `agent.SessionRunner.KickRerender`/`StartOrReattach`/`agent.RefreshResumeSessionID` (the same runner methods either way). The only thing expressed twice is the ~10-line ORDERING of those checks — a small, mechanical sequence (not a fuzzy detection heuristic) that is now captured ONCE for the daemon path in a unit-tested function, with the TUI's copy flagged here as a known, intentional, low-risk residual overlap rather than something nobody decided about.

Alternative considered and rejected: force TUI's `reviveHeraWorker` to call `hera.ReviveRole` too (via a TUI-side `ReviveRunner` adapter using `a.computePTYSize()`). Two adapters CAN satisfy one shared `ReviveRunner` interface with different `KickRerender` sizing, which resolves reason (1) above — but reason (2) (threading) and (3) (scope/regression risk against tested, bug-history-heavy code) still argue against it for this change. Flagged as a reasonable follow-up if the TUI path is ever revisited on its own terms, not attempted here.

### D4 — `ReviveHeraWorkerToInProgress` failure after a successful kick is soft-fail, not propagated

Matches `reviveRestoreInProgress`'s existing behavior exactly (log-and-continue in the TUI; here, simply not treated as a tool error) — the kick itself already succeeded, and a worker stranded in `in_review` can still be closed out manually. Surfacing `kicked_stuck` either way keeps the tool's contract simple (the KICK is the operation being reported on; the in_progress restore is a best-effort side effect of a successful kick, exactly as it already is on the TUI side).

## Risks / Trade-offs

- **[Risk]** The small ordering overlap flagged in D3 could still drift (e.g., a future fix to the TUI's gate that isn't mirrored here). → **Mitigation:** both call sites are documented (this file + a new gotcha bullet) as sharing every underlying primitive except the ordering; a reviewer touching one gets pointed at the other. Low severity: the ordering itself is mechanical (six straight-line boolean checks), not a heuristic prone to the kind of subtle drift this codebase's needs-input detection family has suffered from historically.
- **[Risk]** A coordinator could call `hera_revive` repeatedly in a tight loop against a role that's genuinely just slow. → **Mitigation:** none needed structurally — the idle+blocked gate already refuses to act on a busy or prompt-parked session every single call, so a tight loop just produces repeated `skipped_busy`/`skipped_blocked_on_prompt` no-ops, never a thrash. This mirrors the existing acceptance of "nothing stops calling `hera_status` in a loop either" reasoning from `add-worker-bounce`'s design.
- **[Risk]** Reviving a role whose binding is planned-but-not-yet-materialized (no live binding at all) needs a clear error, not a crash. → **Mitigation:** `HeraLiveBindingByRole` returning `ErrHeraNotFound` is translated to an explicit "no live binding (never spawned, or ended)" tool error, mirroring `resolveOrchRole`'s existing not-found handling.

## Migration Plan

None needed — additive, no schema/data migration, no backwards-compatibility concern.

## Open Questions

None — all decisions above are settled for this change.

## Acceptance criteria

- It should let a coordinator revive a dead (no live session) role of any kind, restarting it in place (resuming via `--session-id` when the task has one).
- It should let a coordinator revive a live-but-stuck (idle, not blocked on a prompt) worker or freelance role by kicking it in place.
- It should restore a kicked worker's task from `in_review` back to `in_progress` when the kick succeeds and the worker isn't awaiting close-out (reusing `ReviveHeraWorkerToInProgress`'s existing guard).
- It should leave a live coordinator role untouched and report `skipped_coordinator_live`, never auto-restarting it.
- It should leave a busy (non-idle) live role untouched and report `skipped_busy`.
- It should leave an idle-but-blocked-on-a-prompt live role untouched and report `skipped_blocked_on_prompt`, never dismissing the pending question.
- It should leave a role with a kick/restart already in flight untouched and report `skipped_restart_pending`.
- It should reject a non-coordinator caller.
- It should reject an unknown role name within the caller's orchestrator.
- It should reject a role name resolving to the caller's own (live, calling) role.
- It should reject a role with no live binding (planned-but-not-materialized, or ended).
