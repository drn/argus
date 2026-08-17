## 1. Implementation

- [x] 1.1 Add `resolveHeraRoleAndCoordinator(taskID string) (role, coord *db.HeraRole, ok bool)` to `internal/daemon/daemon.go`, factoring out the role/coordinator resolution both notification functions share.
- [x] 1.2 Add `heraNotifyService() *hera.Service` factoring out the nil-notifier guard both notification functions share.
- [x] 1.3 Refactor `notifyCoordinatorOfMergedPR` to use the shared resolver (behavior unchanged).
- [x] 1.4 Add `notifyRoleOfMergedPR(t *model.Task, prURL string)`: resolve role/coordinator, no-op when unresolved or when role IS the coordinator (self-send guard), otherwise send the role a self-assessment message ("your PR merged, decide if you have further tasks; if not, inform your coordinator and mark yourself complete via task_complete") with the coordinator as sender.
- [x] 1.5 Wire it into `pollPRStatesOnce`'s merge branch: `if res.Merged { d.notifyCoordinatorOfMergedPR(t, res.URL); d.notifyRoleOfMergedPR(t, res.URL) }` — both fire independently.

## 2. Tests

- [x] 2.1 Keep `TestPollPR_MergedTransitionNudgesCoordinator` unchanged (coordinator nudge behavior is unmodified).
- [x] 2.2 Add a test asserting the role-notify message reaches the worker's own inbox with the PR URL and a `task_complete` mention, alongside the unchanged coordinator nudge.
- [x] 2.3 Add a test asserting the role-notify fires regardless of task status (`in_progress` included, not just `in_review`).
- [x] 2.4 Add a test asserting neither notification fires twice across two poll cycles (terminal-state caching).
- [x] 2.5 Add a test asserting an unmerged close triggers neither notification.
- [x] 2.6 Add a test asserting a task with no resolvable coordinator gets neither notification (no panic).
- [x] 2.7 Add a test asserting a coordinator's own directly-bound merged PR gets neither notification (self-nudge-skip + self-send-guard).
- [x] 2.8 Run `make test-cover` and confirm `internal/daemon` coverage is not regressed.

## 3. Documentation

- [x] 3.1 Update `context/knowledge/gotchas/orchestration.md`'s existing `PRResult.Merged` bullet and add a new bullet documenting the role self-assessment notice, its no-gating design, and the self-send structural limitation.

## 4. Spec archival

- [x] 4.1 Run `openspec archive add-pr-merge-autocomplete` (or the manual merge-and-move fallback) in the same PR, before merge, so `openspec/specs/pr-status/spec.md` reflects the new behavior atomically with the code.

## 5. Verification

- [x] 5.1 Run `make pre-pr` clean.
- [ ] 5.2 Open the PR via `mcp__argus__iris_gh_pr_create`.
