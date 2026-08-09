**Design doc:** `openspec/changes/fix-hera-archive-status/design.md`

## 1. Tests

- [ ] 1.1 `internal/tui` test: nuking a sole-bound role whose task is `in_review` advances the task's status to `complete` (in addition to being archived).
- [ ] 1.2 `internal/tui` test: nuking a sole-bound role whose task is `in_progress` archives it but leaves status `in_progress` (not advanced).
- [ ] 1.3 `internal/tui` test: nuking a sole-bound role whose task is `pending` archives it but leaves status `pending` (not advanced).
- [ ] 1.4 `internal/tui` test: nuking a sole-bound role whose task is already `complete` archives it and leaves status `complete` (idempotent, no error).
- [ ] 1.5 `internal/tui` test: nuking a multi-bound role's task (preserved, not reclaimed) never touches status, mirroring the existing "preserved" test coverage.
- [ ] 1.6 `internal/tui` test: `heraDoCascadeNuke` over a subtree containing an `in_review` worker task and an `in_progress` coordinator task advances only the worker's task to `complete`.
- [ ] 1.7 Confirm every scenario in `specs/hera-view/spec.md`'s "Conservative delete semantics" MODIFIED requirement has a corresponding failing test before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 In `heraReclaimAndArchiveTask` (`internal/tui/heraactions.go`), after fetching `t` and before/alongside the existing `a.db.SetArchived(t.ID, true)` call, add: when `t.Status == model.StatusInReview`, call `a.db.SetStatus(t.ID, model.StatusComplete)`, logging (via `uxlog.Log`) and soft-failing on error exactly like the existing `SetArchived` error handling (never block the archive on a status-write failure).
- [ ] 2.2 Update the function's doc comment to describe the new status-advancement behavior and its guard condition.
- [ ] 2.3 Run `make test-pkg PKG=./internal/tui/` and confirm the new tests from Stage 1 pass.

## 3. Verification

**Depends on:** Stage 2

- [ ] 3.1 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, vuln, test-cover-gate). `govulncheck` failing on a pre-existing Go-toolchain-only stdlib CVE is a documented CI continue-on-error condition, not a blocker.
- [ ] 3.2 `openspec validate fix-hera-archive-status --strict` passes.
- [ ] 3.3 Archive this change into its base spec (`openspec/specs/hera-view/spec.md`) in the same PR, before merge, per this repo's CLAUDE.md.
- [ ] 3.4 Add a one-line gotcha to `context/knowledge/gotchas/hera-view.md` documenting the new status-advancement rule and its exact guard (`in_review` only), cross-referencing `RollHeraWorkerToReview`'s parallel "never clobber active work" invariant.
