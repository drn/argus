**Design doc:** `openspec/changes/fix-nuke-completion-race/design.md`

## 1. Tests

- [x] 1.1 `internal/tui` test: a task `in_progress` at reclaim time, reclaimed via `heraReclaimAndArchiveTask` with a fake runner reporting a live session, still shows `in_progress` immediately after the call (archive-time snapshot untouched — no regression on the existing behavior), then reaches `complete` once `handleSessionExitUI` is driven directly with the non-clean exit the stop produces — the actual regression this change fixes.
- [x] 1.2 `internal/tui` test: the same live-at-reclaim task's marker is consumed (cleared) by that `handleSessionExitUI` call — verify it cannot re-fire on a later, unrelated exit of the same task ID.
- [x] 1.3 `internal/tui` test: a task `in_progress` at reclaim time whose session has NO live runner session at reclaim (`HasSession` false) is archived with status left untouched and the marker does not persist (nothing left to consume it).
- [x] 1.4 `internal/tui` test: a task holding a live worker-kind hera binding still rolls to `in_review` via `RollHeraWorkerToReview` when its exit lands, even with the reclaim marker set — the PR #707 invariant wins over the forced-complete branch (defense-in-depth; not reachable via the normal nuke path today since bindings are ended first, but pinned directly at the `handleSessionExitUI` decision point).
- [x] 1.5 Confirm the existing coverage is unaffected: `TestHeraReclaimAndArchiveTask_InReviewAdvancesToComplete`, `TestHeraReclaimAndArchiveTask_InProgressStatusUntouched`, `TestHeraReclaimAndArchiveTask_PendingStatusUntouched`, `TestHeraReclaimAndArchiveTask_AlreadyCompleteIdempotent`, `TestHeraDoCascadeNuke_StatusAdvanceIsUniformAcrossRoleKinds`, `TestHandleSessionExitUI_SkipsTransitionWhenPendingRestart`, and `TestHandleSessionExitUI_HeraWorkerFinishPolicy` all still pass unmodified.
- [x] 1.6 Confirm every scenario in `specs/hera-view/spec.md`'s MODIFIED requirement has corresponding test coverage before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1

- [x] 2.1 `internal/tui/app.go`: add `pendingHeraReclaim map[string]bool` to `App`, initialized in `New`, guarded by the existing `a.mu`. Add small helper methods (`markHeraReclaimPending`, `clearHeraReclaimPending`, `consumeHeraReclaimPending`) that lock/unlock around the map access.
- [x] 2.2 `internal/tui/heraactions.go`: in `heraReclaimAndArchiveTask`, when `t.Status == model.StatusInProgress`, call `markHeraReclaimPending` before the `heraGoSafe` stop goroutine is spawned. Inside that goroutine, clear the mark on the "no live session" branch and on a `Stop()` error — both guarantee no future exit notification is coming for this stop attempt.
- [x] 2.3 `internal/tui/app.go`: in `handleSessionExitUI`'s `StatusInProgress` / `!pendingRestart` branch, consume the marker once (before or alongside the existing `RollHeraWorkerToReview` call) and, when set and the roll didn't fire, force `StatusComplete` instead of the ordinary clean/non-clean rule.
- [x] 2.4 Update `heraReclaimAndArchiveTask`'s doc comment to describe the new marker and its consumption point.
- [x] 2.5 Run `make test-pkg PKG=./internal/tui/` and confirm the new tests from Stage 1 pass.

## 3. Verification

**Depends on:** Stage 2

- [x] 3.1 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, vuln, test-cover-gate). The documented ARGUS_* env-leak false-fail on 2 unrelated `internal/agent` tests inside a hera-worker sandbox is pre-existing — confirm clean with those excluded, don't chase it.
- [x] 3.2 `openspec validate fix-nuke-completion-race --strict` passes.
- [x] 3.3 Archive this change into its base spec (`openspec/specs/hera-view/spec.md`) in the same branch, before the final commit, per this repo's CLAUDE.md.
- [x] 3.4 Add a gotcha to `context/knowledge/gotchas/hera-view.md` documenting the marker, its exact scope (`in_progress`-at-reclaim only), and the compound-race limitation noted in design.md's Risks section. Bump the bullet count in `context/knowledge/index.md`'s hera-view row.
