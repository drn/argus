## 1. Fix

- [x] 1.1 `internal/tui/hera/panes.go`: `bindPane` and `reconcileOne` call `tp.ResetVT()` before `tp.SetSession(sess)` on every genuine session swap
- [x] 1.2 `internal/tui/heraactions.go`: `heraDoForceRecycle` stages `a.pendingRerenderRestart[taskID]` before invoking `RecycleCoord`, rolling it back if `RecycleCoord` errors before touching the session
- [x] 1.3 `internal/tui/app.go`: `handleSessionExitUI`'s immediate-resume branch calls `a.agentPane.ResetVT()` before `a.startSession(t)`
- [x] 1.4 `internal/tui/terminal/terminalpane.go`: the "No active session" / "Session not running" / "Waiting for output..." DrawText-only branches fill their full bounding rect first

## 2. Tests

- [x] 2.1 `internal/tui/hera/panes_test.go`: `TestPanes_ReconcileResetsVTOnSessionSwap` — a same-task session swap (recycle shape) resets the pane's VT state (diff-mode used as a deterministic, exported-API-only proxy) and the swap is confirmed to fail before the fix, pass after
- [x] 2.2 `internal/tui/heraactions_test.go`: `TestHeraActions_ForceRecycleCleansUpPendingFlagOnError` — a failed recycle attempt does not leak a `pendingRerenderRestart` entry

## 3. Verification

- [x] 3.1 `make build`, `make vet`, `make fmt-check`, `make lint-pr` clean
- [x] 3.2 `make test-cover-gate` (full race suite) green, coverage floor met
- [x] 3.3 `make vuln` advisory-only stdlib gap noted, not chased (CI runs it continue-on-error)
