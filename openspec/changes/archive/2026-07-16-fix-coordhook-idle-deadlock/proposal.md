## Why

`cmd/argus/coord_hook.go`'s `runCoordHook` — the Claude Code `Stop` hook that nudges an over-budget coordinator to recycle — re-evaluates fresh on every invocation with zero memory of prior calls. Combined with `internal/hera/recycle.go`'s `RecycleCoord` self-service path (a no-op unless `runner.IsIdle(taskID)`, which itself requires 3+ consecutive seconds of PTY silence), this produces a real, observed infinite loop: hook blocks -> coordinator complies and calls `hera_status(request_recycle=true)` -> hook fires again on the very next Stop (still over budget, has no idea recycle was already requested) -> the block decision forces immediate re-engagement, so the PTY never accumulates 3s of silence -> `RecycleWatcher` never sees `IsIdle()==true` -> the recycle never actually fires. This happened live today: 15+ consecutive loop iterations, budget climbing from ~221K to ~267K tokens, before a human manually disabled the hook.

## What Changes

- `runCoordHook` now checks `task_meta(hera, pending_recycle)` before emitting a block decision. If it's already `"true"`, the hook returns with no decision (Claude Code treats no stdout JSON as "allow stop"), letting the turn genuinely end so `RecycleWatcher` gets a real idle window. This is the idempotency fix — Part A.
- New hard-stop escalation (Part B): once a coordinator's `context_size` crosses 1.5x its configured budget, the hook calls a new daemon RPC verb, `Daemon.ForceRecycleCoordinator`, which immediately kills and restarts the coordinator's session via `recycle_coord`'s human-forced path (`hera.RecycleHumanForced`) — bypassing the idle gate entirely. This fires regardless of whether `pending_recycle` is already set; it's an unconditional safety net for the case where a human keeps replying quickly enough that the session never naturally goes idle even with Part A in place.
- `Daemon.ForceRecycleCoordinator` mirrors `internal/tui/heraactions.go`'s `heraDoForceRecycle` (the rail's `B` key) exactly — same coordinator-role resolution via `ListHeraLiveBindingsByTask`, same `daemon.NewHeraRecycleRunner`, same `hera.RecycleCoord(..., RecycleHumanForced)` call — so both entry points behave identically.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `coordinator-context-management`: the "Context-budget Stop hook stamps a live signal and nudges over budget" requirement gains an idempotency clause (no re-block once `pending_recycle` is already true) and a new "hard-stop escalation at 1.5x budget" requirement is added.

## Impact

- **Code:** `cmd/argus/coord_hook.go` (`coordHookEnv` gains `PendingRecycleAlready` + `ForceRecycle` fields; `runCoordHook` gains the pending-recycle check and the hard-stop threshold check; new `pendingRecycleAlreadyReal` + `forceRecycleReal`), `internal/daemon/rpc.go` (new `RPCService.ForceRecycleCoordinator` RPC method).
- **Tests:** `cmd/argus/coord_hook_test.go` (regression test for the incident, hard-stop threshold boundary tests, RPC-call-wiring tests via the injected fake env), `internal/daemon/force_recycle_test.go` (new RPC method's happy path + error cases).
- **No schema/migration, no REST/TUI/macOS surface change** — the new RPC verb is daemon-socket-only, called by the `coord-hook` CLI subcommand exactly the way `Daemon.Ports` already is; no protocol version bump (it's a plain daemon-registered method like `KBSearch`/`ClipboardSet`, not part of the session-supervisor R/S handshake protocol).
