**Design doc:** `openspec/changes/fix-hera-view-residual-drift/design.md`

This change is almost entirely comment/spec-text edits (no behavior change).
Stage 1 fixes the two stale Go doc comments; Stage 2 corrects the two spec
requirements; Stage 3 verifies and archives.

## 1. Code comment fixes

- [x] 1.1 In `internal/tui/hera/rail.go:1628-1645`, rewrite the doc comment above `statusIcon`/`roleStatusInputs` to describe the actual precedence (`NeedsInput > Active > ReadyToClose > Failed > Done > Idle > Live > default`, `Active` = `Live && SessionRunning && !SessionIdle`) instead of the pre-#824 "ready_to_close wins over everything... bound argus task is in_progress" framing. Cite `widget/rolestatusicon.go` per D1.
- [x] 1.2 In `internal/tui/hera/model.go:1031-1034`, rewrite `buildRoleView`'s comment to say needs-input surfaces "for a WORKER or COORDINATOR role" instead of "worker, coordinator, or freelance," matching the corrected spec text and `needsInputForHeraRail`'s actual admission scope, per D2.
- [x] 1.3 Confirm neither edit changes any code logic (`git diff` shows comment-only hunks) and `go build ./...` / `make vet` pass.

## 2. Spec corrections

**Depends on:** none (independent of Stage 1)

- [x] 2.1 Apply the delta at `openspec/changes/fix-hera-view-residual-drift/specs/hera-view/spec.md`'s "Needs-input summary box above the rail" MODIFIED requirement — confirm the rewritten paragraph accurately describes the box's `in_progress`-only gate as deliberately narrower than (not identical to) the per-role rollup's `in_progress OR live` gate, per D3.
- [x] 2.2 Apply the delta's "Live plan node icons are 1:1 with the rail" MODIFIED requirement and its "Needs-input outranks active without animating (BUG-012)" scenario — confirm the reworded WHEN clause references the actual `Live && SessionRunning && !SessionIdle` definition rather than using `in_progress` as a stand-in, per D4.
- [x] 2.3 Confirm `openspec validate fix-hera-view-residual-drift --strict` passes.

## 3. Verify and archive

**Depends on:** Stage 1, Stage 2

- [x] 3.1 Re-read both corrected spec requirements against `internal/tui/hera/model.go` (`buildRoleView`, `IsActive`), `internal/tui/app.go` (`needsInputInProgress`, `needsInputForHeraRail`), and `internal/tui/hera/plan.go` (needs-input-outranks-active handling) to confirm the new prose matches the code exactly.
- [x] 3.2 Run `make pre-pr` and confirm it passes clean (comment-only Go changes must not affect build/vet/lint/tests).
- [x] 3.3 Run `openspec archive fix-hera-view-residual-drift` (merges the delta into `openspec/specs/hera-view/spec.md`, moves the change folder to `openspec/changes/archive/<date>-fix-hera-view-residual-drift/`), committed on this branch.
- [x] 3.4 Run `openspec validate --all --strict` and confirm it passes against the merged base spec.
