# Tasks — fix-bug-b-revive-status

TDD: write failing tests from the `hera-coordination` delta first, then
implement. Verify with `make pre-pr` (full CI mirror) before the PR.

## 1. Tests (RED)

- [x] 1.1 `internal/db`: `TestReviveHeraWorkerToInProgress` — worker in_review,
      no terminal markers → flips to in_progress (true); + ready_to_close → no-op;
      role-status done → no-op; role-status failed → no-op; in_progress → no-op;
      complete → no-op; coordinator-kind → no-op; no binding → no-op; idempotent.
- [x] 1.2 `internal/daemon`: `TestReconcileOnStartup_Supervised_RevivesStrandedLiveWorker`
      — a live (in liveSet) worker in in_review with no terminal markers is
      restored to in_progress on reattach; regression: a live worker with
      ready_to_close stays in_review.
- [x] 1.3 `internal/tui`: `TestReviveRestoreInProgress` — App in local mode
      restores a revived worker (in_review, no terminal) to in_progress; a
      ready_to_close worker stays in_review.

## 2. Implement (GREEN)

- [x] 2.1 `internal/db/hera.go`: add `ReviveHeraWorkerToInProgress` + the
      `heraWorkerAwaitingCloseout` terminal-state predicate.
- [x] 2.2 `internal/daemon/bounce.go`: in `reattachSupervised`, after the
      re-attach loop, call the helper for each live task; log restorations.
- [x] 2.3 `internal/tui/app.go`: add `reviveRestoreInProgress(taskID)` (local
      type-assert to `*db.DB`); call it from `reviveHeraWorker`'s success branch
      in `internal/tui/heraactions.go`.

## 3. Verify + document

- [x] 3.1 `make pre-pr` green.
- [x] 3.2 Document the invariant in `context/knowledge/gotchas/daemon-rpc.md`.
- [x] 3.3 `openspec validate --all --strict` passes; archive within the branch.
