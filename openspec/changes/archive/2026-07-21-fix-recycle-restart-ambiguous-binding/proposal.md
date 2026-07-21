## Why

**`recycle_coord`'s self-service path permanently fails for any task holding
2+ live hera bindings** — e.g. a task that is a worker in one orchestrator
and a coordinator in another, an already-supported configuration. The daemon
log shows the recycle watcher retrying every 5s tick forever:

```
[hera] recycle watcher: tick failed task=<id> err="recycle_coord: restart task <id>: recycle restart: resolve binding for task <id>: hera: lookup ambiguous (multiple live bindings match)"
```

Root cause: `RecycleWatcher.tickTask` and `RecycleCoord` already disambiguate
a dual-bound task down to one `roleID` (preferring the coordinator-kind
binding) before calling the daemon's kill/restart primitive. But the
`RecycleRunner.Restart(taskID string) error` interface only carried
`taskID`, dropping that already-resolved role. The concrete implementation,
`daemon.HeraRecycleRunner.Restart`, re-derived the binding itself via the
single-binding `HeraLiveBindingByTask(taskID)` — which returns
`ErrHeraAmbiguous` for exactly the case the caller had already solved,
permanently wedging that task's pending-recycle intent. It can never be
consumed for that task.

## What Changes

- **`hera.RecycleRunner.Restart` widened to `Restart(taskID string, roleID
  int64) error`.** `RecycleCoord` passes the `roleID` it already resolved
  (from `store.HeraLiveBindingByRole`) straight through instead of dropping
  it.
- **`daemon.HeraRecycleRunner.Restart` resolves via
  `HeraLiveBindingByRole(roleID)`**, never re-deriving the binding from
  `taskID` alone. No other call site (the daemon RPC's
  `ForceRecycleCoordinator`, the TUI rail's `B` key) calls `Restart`
  directly — both route exclusively through `RecycleCoord`, so both are
  fixed by this one change.
- **Test doubles** (`fakeRecycleRunner`, `concurrentRecycleRunner`) updated
  to the new signature.

## Capabilities

### Modified Capabilities

- `coordinator-context-management`: `recycle_coord`'s kill-and-restart step
  now resolves the outgoing binding via the already-disambiguated role
  rather than re-deriving it from the task ID, so a task holding 2+ live
  hera bindings can actually complete a self-service (or human-forced)
  recycle instead of wedging forever.

## Impact

- **Modified code:** `internal/hera/recycle.go` (interface + call site),
  `internal/daemon/recycle.go` (`HeraRecycleRunner.Restart`).
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  bugfix restoring already-specified multi-binding disambiguation behavior
  through to the actual restart.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
