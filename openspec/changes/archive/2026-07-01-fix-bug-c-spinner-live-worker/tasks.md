## 1. Gate the working spinner on a running, non-idle session

- [x] 1.1 `RoleView.IsActive()` (internal/tui/hera/model.go): change the predicate to `r.Live && r.SessionRunning && !r.SessionIdle`, dropping the `TaskStatus == in_progress` clause; doc comment explains BUG-003 is guarded by session-running (bindings outlive session exit, so liveness alone can't) and BUG-036 by `SessionIdle`.
- [x] 1.2 New `RoleView.SessionRunning` signal: add the field; add a `sessionRunning map[string]bool` param to `BuildModel`/`buildRoleView` (keyed by live task ID); add `HeraPage.SetSessionRunning` + page field; push `runningIDs` from `App.refreshTasksWithIDs` via `a.heraPage.SetSessionRunning`.
- [x] 1.3 `coordRoleStatusLabel` (internal/tui/hera/details.go): no logic change (it already delegates to `IsActive`); refresh the doc comment for the running + content-idle contract.

## 2. Tests (TDD)

- [x] 2.1 Repro at the BuildModel seam (`TestBuildModel_LiveWorkerInReviewSpins`): a live worker rolled to `in_review` with a RUNNING session, NOT content-idle → `IsActive()==true`; content-idle → false; NOT running (dead, binding lingering) → false (the BUG-003 regression case).
- [x] 2.2 Predicate unit test (`TestRoleView_IsActive`): reframe to the new contract — live+running+active spins regardless of task status; live+SessionIdle does not (BUG-036); live-but-not-running does not (BUG-003, dead worker); not-Live does not.
- [x] 2.3 Render test (`TestStatusIcon_ActiveAnimatesSpinner`): a live+running `in_review` role still producing animates; live+SessionIdle and live-but-not-running roles stay static.
- [x] 2.4 Reframe the coordinator status-label tests (`TestCoordRoleStatusLabel`, `TestCoordStatusLabel_Combined`): live+running+active in_review reads "working"; live+SessionIdle and live-but-not-running read "live".

## 3. Docs & gates

- [x] 3.1 Document the invariant in context/knowledge/gotchas/hera-view.md.
- [x] 3.2 `make pre-pr` green; `openspec validate --all --strict` passes.
- [x] 3.3 Archive this change within the branch (base specs updated atomically).
