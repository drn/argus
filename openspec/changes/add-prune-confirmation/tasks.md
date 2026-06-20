# Tasks

## 1. Confirmation gate

- [x] 1.1 Add `modeConfirmPrune` to the `viewMode` enum and a `confirmPruneModal *modal.ConfirmModal` field to `App`.
- [x] 1.2 Add `openConfirmPrune()`: re-entrancy guard (header notice), count completed tasks from `a.tasks`, skip with a status note when zero, else open a `modal.ConfirmModal` naming the count.
- [x] 1.3 Add `handleConfirmPruneKey()` (confirm → close then `pruneCompletedTasks`; cancel → close) and `closeConfirmPrune()`.
- [x] 1.4 Point the `Ctrl+R` Tasks-tab handler at `openConfirmPrune()` instead of `pruneCompletedTasks()`.
- [x] 1.5 Dispatch `modeConfirmPrune` in the App InputCapture, alongside the other confirm modals.

## 2. Tests

- [x] 2.1 `TestCtrlROpensPruneConfirm`: Ctrl+R opens the modal and prunes nothing yet.
- [x] 2.2 `TestPruneConfirm_YPrunes`: confirming with `y` prunes completed tasks and dismisses the modal.
- [x] 2.3 `TestPruneConfirm_NCancels`: cancelling with `Esc` leaves all tasks and dismisses the modal.
- [x] 2.4 `TestPruneConfirm_NoCompletedSkipsModal`: Ctrl+R with no completed tasks opens no modal.

## 3. Docs

- [x] 3.1 Record the gotcha (prune is now confirm-gated; Ctrl+R key/label unchanged so no help-modal edit) in `context/knowledge/gotchas/tasklist-ui.md`.
