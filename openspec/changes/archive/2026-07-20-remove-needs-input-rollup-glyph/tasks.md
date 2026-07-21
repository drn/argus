**Design doc:** openspec/changes/remove-needs-input-rollup-glyph/design.md

## 1. Tests

- [x] 1.1 `internal/tui/hera/rail_test.go`: in `TestStatusIcon_NeedsInputSources`, flip `"subtree rollup shows (?) on an otherwise-idle coordinator"` to assert the coordinator's OWN status glyph shows (not needs-input) when only `SubtreeNeedsInput` is set; flip `"rollup beats a done coordinator"` to assert the `done` glyph shows, not needs-input; fix `"needs-input rollup wins over ready_to_close (BUG-A)"` to construct its `RoleView` with `NeedsInput: true` instead of `SubtreeNeedsInput: true` (the real own-signal field), keeping its needs-input-beats-ready_to_close assertion unchanged
- [x] 1.2 `internal/tui/hera/plan_test.go`: in `TestPlanNodeIcon_NeedsInputNotAnimated`, rewrite the `"descendant subtree rollup"` subtest to assert the bridging sub-coordinator node `w` (genuinely active, descendant `wc` blocked) renders the ACTIVE SPINNER (animated), not the static needs-input glyph — update the subtest's doc comment to match
- [x] 1.3 `internal/tui/bug028_integration_test.go`: correct the docstrings/inline comments on `TestBUG028_Integration_HeraRailShowsNeedsInputForBlockedWorker` and `TestBUG028_Integration_CoordinatorlessHeaderSurfacesNeedsInput` to state the glyph is now supplied by the revealed descendant row, not the header — assertions (`screenHasRune`) are unchanged and both tests must keep passing
- [x] 1.4 Add new tests in `internal/tui/hera/details_test.go`: (a) a coordinator with its OWN needs-input signal renders the needs-input glyph on the Details `coordinator:` status line; (b) a coordinator with ONLY a blocked descendant (no own signal) does NOT render the needs-input glyph on that line; (c) the equivalent pair for a roster row via `rosterStatusText`/`drawRosterRow` — own signal shows `"needs-input"` + glyph, descendant-only rollup does not
- [x] 1.5 Confirm (read, do not yet change) `internal/tui/hera/model_test.go`'s `TestRollupNeedsInput_*` family, `internal/tui/hera/jumpneedsinput_test.go`, `internal/tui/hera/rail_reveal_test.go`, and `internal/tui/hera/page_test.go` need NO changes — they test the `SubtreeNeedsInput` COMPUTATION and the reveal/ctrl+g mechanisms, not the now-removed display use, and must still pass unmodified after Stage 2
- [x] 1.6 Run `make test-pkg PKG=./internal/tui/hera/` and `make test-pkg PKG=./internal/tui/` and confirm every test touched in 1.1-1.4 fails against the current (pre-fix) code, for the right reason (Prove-It Pattern)

## 2. Implementation

**Depends on:** Stage 1

- [x] 2.1 `internal/tui/hera/model.go`: change `RoleView.ShowsNeedsInput()` to return `r.needsInputOwn()` alone (drop `r.SubtreeNeedsInput ||`); update its doc comment and the `SubtreeNeedsInput`/`OrchView.SubtreeNeedsInput` field comments to state the field now exists solely to gate the fold-reveal mechanism, not to drive display; update `rollupNeedsInput`'s comment referencing "so the rail can project '(?)' up the tree" / BUG-028 accordingly
- [x] 2.2 `internal/tui/hera/rail.go`: remove `drawOrchRow`'s `else if o.SubtreeNeedsInput { ... }` branch entirely (the BUG-028 coordinator-less fallback) — leave the `if coord := o.CoordRole(); coord != nil { ... }` branch with no `else`; correct the stale comments on `statusIcon`/`roleStatusInputs` (rollup-driven precedence) and `needsInputTaskID` ("a folded header always shows for any descendant") to describe the new behavior
- [x] 2.3 Confirm no change needed in `internal/tui/hera/details.go` or `internal/tui/hera/plan.go` — both already read the shared classifier (`roleStatusInputs`/`statusIcon`) and are fixed by 2.1 alone; read both files once more after 2.1 to verify no independent `SubtreeNeedsInput` read was missed
- [x] 2.4 Run `make test-pkg PKG=./internal/tui/hera/` and `make test-pkg PKG=./internal/tui/` and confirm every test from Stage 1 now passes, with no regressions in the surrounding suite

## 3. Verification & archive

**Depends on:** Stage 2

- [x] 3.1 `openspec validate remove-needs-input-rollup-glyph --strict`
- [x] 3.2 `make test-cover` on `internal/tui/hera` and `internal/tui` — confirm the touched packages stay at or above the coverage floor
- [x] 3.3 `make pre-pr` (full CI-mirroring gate) — must pass clean before opening/updating the PR
- [x] 3.4 Archive within the same PR before merge: `openspec archive remove-needs-input-rollup-glyph` (merges the `hera-view` deltas into `openspec/specs/hera-view/spec.md` and moves the change folder to `openspec/changes/archive/<date>-remove-needs-input-rollup-glyph/`), commit the result on the change branch
- [x] 3.5 Open the PR via `iris_gh_pr_create` and report back to the parent coordinator
