## 1. Fix

- [x] 1.1 `internal/hera/recycle.go`: widen `RecycleRunner.Restart` to `Restart(taskID string, roleID int64) error`; `RecycleCoord` passes its already-resolved `roleID` through
- [x] 1.2 `internal/daemon/recycle.go`: `HeraRecycleRunner.Restart` accepts `roleID` and resolves via `HeraLiveBindingByRole(roleID)` instead of the ambiguous `HeraLiveBindingByTask(taskID)`
- [x] 1.3 Confirmed no other `RecycleRunner.Restart` call site exists — the daemon RPC's `ForceRecycleCoordinator` and the TUI rail's `B` key both route exclusively through `RecycleCoord`, never call `Restart` directly

## 2. Tests

- [x] 2.1 `internal/daemon/recycle_test.go`: `TestHeraRecycleRunner_Restart_DualLiveBindingResolvesViaRoleID` — a task bound as coordinator in one orchestrator and worker in another restarts successfully via the real `HeraRecycleRunner`; confirmed to fail with the original `hera: lookup ambiguous` error before the fix, pass after
- [x] 2.2 Existing call sites (`fakeRecycleRunner`, `concurrentRecycleRunner`, `TestRecycleWatcher_Tick_MultiBindingTaskFindsCoordinatorBinding`) updated to the new signature; all pass

## 3. Verification

- [x] 3.1 `make build`, `make vet`, `make fmt-check`, `make lint-pr` clean
- [x] 3.2 `make test-cover-gate` (full race suite) green, coverage floor met
- [x] 3.3 `make vuln` advisory-only stdlib gap noted, not chased (CI runs it continue-on-error)
