**Design doc:** `openspec/changes/fix-coordhook-idle-deadlock/design.md`

## 1. Part A: idempotent coord-hook

- [x] 1.1 In `cmd/argus/coord_hook_test.go`, extend `coordHookEnv`/`fakeCoordHookEnv` with `PendingRecycleAlready`/`pendingRecycle` and add `TestCoordHook_Coordinator_PendingRecycleAlready_DoesNotBlock` (the regression test for the incident: over-budget + `pending_recycle=true` must NOT emit a block decision) and `TestCoordHook_PendingRecycleAlready_ReadError_StillBlocks` (a read error falls back to the graceful block, not a silent no-op). Confirm they fail against the current implementation (no `PendingRecycleAlready` field exists yet).
- [x] 1.2 Update existing tests (`TestCoordHook_NoTaskID_NoOp`, `TestCoordHook_NonCoordinatorRole_NoOp`, `TestCoordHook_Coordinator_AlwaysStampsContextSize`, `TestCoordHook_Coordinator_OverBudget_EmitsNudge`) to assert `pendingRecycleCalled` is `false` where the pending-recycle check should never be reached (no-op paths, under-budget path).
- [x] 1.3 Implement `PendingRecycleAlready func(taskID string) (bool, error)` on `coordHookEnv`, wire it into `runCoordHook` (checked after the budget/hard-stop check, before emitting the block decision), and add the real implementation `pendingRecycleAlreadyReal` (a second `GET /api/tasks/{id}/meta?namespace=hera` read, checking `db.HeraMetaKeyPendingRecycle == "true"`).
- [x] 1.4 Run `make test-pkg PKG=./cmd/argus/` — all Part A tests pass, no regressions.

## 2. Part B: daemon RPC ForceRecycleCoordinator

**Depends on:** none (independent of Stage 1)

- [x] 2.1 In `internal/daemon/force_recycle_test.go`, add `TestForceRecycleCoordinator_HappyPath` (task bound to a coordinator role, no live session -> force-recycle starts a fresh one), `TestForceRecycleCoordinator_UnknownTask_Errors`, and `TestForceRecycleCoordinator_WorkerOnly_Errors` (a task bound only to a worker-kind role must error, not silently no-op). Confirm they fail to compile (`ForceRecycleCoordinator` doesn't exist yet).
- [x] 2.2 Implement `RPCService.ForceRecycleCoordinator(req *TaskIDReq, resp *StatusResp) error` in `internal/daemon/rpc.go`: resolve the coordinator-kind role bound to `req.TaskID` via `ListHeraLiveBindingsByTask` + `HeraRole` (same shape as `hera.RecycleWatcher.tickTask`), build a `NewHeraRecycleRunner(s.daemon.db, s.runner, s.cfgFn)`, and call `hera.RecycleCoord(s.daemon.db, rr, coordRoleID, sessionID, hera.RecycleHumanForced)`.
- [x] 2.3 Run `make test-pkg PKG=./internal/daemon/` — all Stage 2 tests pass.

## 3. Part B: coord-hook hard-stop threshold

**Depends on:** Stage 2 (needs `Daemon.ForceRecycleCoordinator` to call)

- [x] 3.1 In `cmd/argus/coord_hook_test.go`, add `TestCoordHook_HardStop_JustUnderThreshold_DoesNotForceRecycle` and `TestCoordHook_HardStop_AtThreshold_ForcesRecycle` (pinning the integer-safe `size*2 >= budget*3` boundary), `TestCoordHook_HardStop_FiresRegardlessOfPendingRecycle` (escalation fires even when `pending_recycle` is already true, and skips reading it), and `TestCoordHook_HardStop_ForceRecycleError_LogsToStderr` (an RPC failure logs but doesn't crash or fall back to the graceful block).
- [x] 3.2 Add `ForceRecycle func(taskID string) error` to `coordHookEnv`, wire the hard-stop check into `runCoordHook` (before the pending-recycle check; short-circuits it), and add the real implementation `forceRecycleReal` (dials the daemon via `coordHookDial`, calls `Daemon.ForceRecycleCoordinator` with a `daemon.TaskIDReq`).
- [x] 3.3 Run `make test-pkg PKG=./cmd/argus/` — all Stage 3 tests pass, including Stage 1's.

## 4. Spec + verification

**Depends on:** Stage 3

- [x] 4.1 Write the delta spec at `openspec/changes/fix-coordhook-idle-deadlock/specs/coordinator-context-management/spec.md`: MODIFIED "Context-budget Stop hook stamps a live signal and nudges over budget" (idempotency clause + scenario), ADDED "Hard-stop escalation forces a recycle at 1.5x budget" (new requirement + scenarios).
- [x] 4.2 Archive in the same commit: merge the delta into `openspec/specs/coordinator-context-management/spec.md` and move this change folder to `openspec/changes/archive/2026-07-16-fix-coordhook-idle-deadlock/`.
- [x] 4.3 `make pre-pr` passes clean.
