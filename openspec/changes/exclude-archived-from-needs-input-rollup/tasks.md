**Design doc:** `openspec/changes/exclude-archived-from-needs-input-rollup/design.md`

## 1. Tests

- [ ] 1.1 In `internal/tui/hera/model_test.go`, add `TestRollupNeedsInput_ArchivedLeafExcludedFromParent`: a leaf worker directly under a coordinator is in needs-input; archive its role; assert the coordinator's `SubtreeNeedsInput` is now `false`.
- [ ] 1.2 Add `TestRollupNeedsInput_ArchivedLeafExcludedAcrossMultipleBridgeLevels`: reuse the `R → C → G` fixture from `TestRollupNeedsInput_BubblesToParentAndRoot`; the deepest leaf needs input and bubbles to the root; archive the leaf's role; assert `C`, `R`, and the intervening bridging worker rows all report `false`.
- [ ] 1.3 Add `TestRollupNeedsInput_ArchivedBridgingRowHidesSubtree`: a nested sub-coordinator's bridging row is itself NOT in needs-input, but a worker within its bridged child orchestrator IS; archive the bridging row's role; assert the parent (and any further ancestor) reports `false` even though the child's worker is still genuinely in needs-input.
- [ ] 1.4 Add `TestRollupNeedsInput_ArchivedOrchestratorExcludedViaWorkerBridge`: a child orchestrator reached via a worker-bridge is itself archived (`OrchView.Archived = true`) and contains a blocked worker; assert the live parent's rollup reports `false`.
- [ ] 1.5 Add `TestRollupNeedsInput_ArchivedRoleStillShowsOwnGlyph`: a worker's role is archived while genuinely in needs-input; assert `ShowsNeedsInput()` (or the role's own `needsInputOwn()`/`SubtreeNeedsInput` as rendered on its own row) is still `true` on that role itself.
- [ ] 1.6 Add `TestRollupNeedsInput_ArchivedCycleSafe`: extend the existing bridge-cycle fixture (`TestRollupNeedsInput_CycleSafe`) with one cyclic member archived; assert the rollup still terminates and reports correctly for the reachable, non-archived members.
- [ ] 1.7 Confirm all six new tests FAIL against the current `orchSubtreeNeedsInput` (prove the gap exists) before implementing.

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 In `internal/tui/hera/model.go`, replace `orchSubtreeNeedsInput`'s body with the dedicated archive-aware recursive walk from `design.md` (visited-set cycle guard; skips a role entirely — its own signal AND descent through it — when `role.Archived` is true; still descends into `coordBridgeChildren`, which already excludes archived orchestrators).
- [ ] 2.2 Verify `rollupNeedsInput` (phase 1 and phase 2) needs no changes — confirm each role's own `SubtreeNeedsInput` stamping still reflects its own state (including an archived bridging role's own hidden-subtree glyph) per design.md's note that only ancestor-facing calls change.
- [ ] 2.3 Run `make test-pkg PKG=./internal/tui/hera/` and confirm all six new tests pass along with the full existing suite (no regression on `TestRollupNeedsInput_BubblesToParentAndRoot`, `_BlockedStatusCounts`, `_BlockedClearsToRoot`, `_NoFalsePositive`, `_CoordSpawnedSubteam`, `_CycleSafe`).

## 3. Documentation and gate

**Depends on:** Stage 2

- [ ] 3.1 Add a bullet to `context/knowledge/gotchas/hera-view.md`'s needs-input rollup coverage noting the archived-role/archived-bridge exclusion and that it required a dedicated traversal (not `BridgeSubtree` reuse) because `BridgeSubtree` must keep including archived rows for rendering.
- [ ] 3.2 Update `context/knowledge/index.md`'s `hera-view.md` row bullet count/summary to reflect the new coverage.
- [ ] 3.3 Run `make pre-pr` and confirm it passes clean.
- [ ] 3.4 Run `openspec archive exclude-archived-from-needs-input-rollup` (merge the delta into `openspec/specs/hera-view/spec.md` and move the change folder to `openspec/changes/archive/<date>-exclude-archived-from-needs-input-rollup/`), committed on this branch before the PR merges.
