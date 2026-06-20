## 1. DB layer

- [x] 1.1 Add `db.ClearHeraReadyToClose(taskID)` — inverse of the
  `RollHeraWorkerToReview` stamp (sets `meta:hera.ready_to_close=false`,
  meta-only, idempotent no-op when never set).
- [x] 1.2 Cover it in `internal/db/hera_m4_test.go` (clears after a roll;
  idempotent no-op when never set).

## 2. Mutation layer

- [x] 2.1 Add `ClearHeraReadyToClose` to the `MutateStore` interface.
- [x] 2.2 In `Ops.StepStatus`, when a WORKER role steps to a non-`done` status,
  clear the mark (soft-fail). Keep the done-roll on the `done` branch.
- [x] 2.3 Cover the revert-out-of-done clear and the advance-to-done keep in
  `internal/tui/hera/bug024_test.go`.

## 3. Regression guards

- [x] 3.1 End-to-end rail test: stepping the selected worker out of review keeps
  the cursor anchored on that worker, visibly changes its glyph, and leaves the
  coordinator's numeric `(N)` count badge intact.
