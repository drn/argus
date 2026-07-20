**Design doc:** openspec/changes/fix-ctrlg-coordinator-own-need/design.md

## 1. Tests

- [ ] 1.1 Flip `TestRail_NextNeedsInputTaskID_TopLevelCoordinatorOwnNeedIsNotACandidate` (`internal/tui/hera/jumpneedsinput_test.go`) to assert the coordinator's own need IS now a reachable candidate (`ok==true`, `id=="tc"`); rename to reflect the new contract and rewrite its docstring
- [ ] 1.2 Add a test asserting `Rail.SelectByTaskID` resolves a top-level coordinator's own task id directly to its header row (`Selection().Role == nil`, `Selection().Orch` matches, `IsCoordinator()==true`)
- [ ] 1.3 Add an end-to-end test: `HeraPage.JumpToNextNeedsInput` lands on a top-level coordinator's own need — ancestors unaffected (it's a root), focus moves to the coordinator pane (`FocusCoord`), mirroring the existing `TestHeraPage_JumpToNextNeedsInput_ExpandsAncestorAndSelects` shape
- [ ] 1.4 Add a test for the coordinator-spawned nested sub-team shape: seed a parent/child orchestrator pair sharing one coordinator task via the coord-spawn canonical-parent relationship (mirror the seeding pattern in `TestModel_CoordSpawnedSubteamBridge`, `internal/tui/hera/model_test.go`), mark the child's coordinator task as needing input, and confirm `ctrl+g` reaches it (expanding a collapsed parent ancestor first)
- [ ] 1.5 Add a test for the shared-task multi-header edge case (design.md Decision 3): parent and child headers share one coordinator task that needs input; confirm `NextNeedsInputTaskID`/`SelectByTaskID` consistently resolve to the same first-matching header rather than alternating, and that this doesn't produce a crash or an infinite-advancing cursor
- [ ] 1.6 Update the now-stale doc comments in `internal/tui/hera/rail.go` (`needsInputTaskID`'s docstring, and any nearby comment asserting the old "deliberately excluded"/"could never land" framing) so they describe intent rather than lying about it mid-implementation
- [ ] 1.7 Run `make test-pkg PKG=./internal/tui/hera/` and confirm every test added/changed above fails against the current (pre-fix) code, for the right reason (Prove-It Pattern)

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 Extend `railRow.needsInputTaskID()` (`internal/tui/hera/rail.go`) with an `rrOrch` branch: resolve `coord := row.orch.CoordRole()`; qualify when `coord != nil && coord.needsInputOwn() && coord.TaskID != ""`, returning `coord.TaskID`
- [ ] 2.2 Extend `Rail.SelectByTaskID()` (`internal/tui/hera/rail.go`) with a second match pass, run when the existing role-row scan finds nothing: scan for a header row (`row.kind == rrOrch`) whose `row.orch.CoordRole()` is non-nil and has `TaskID == taskID`, landing the cursor there via the existing `setCursor(i)` call
- [ ] 2.3 Run `make test-pkg PKG=./internal/tui/hera/` and confirm every test from Stage 1 now passes, with no regressions in the surrounding suite (`rail_test.go`, `model_test.go`, `page_test.go` or equivalents)

## 3. Verification & archive

**Depends on:** Stage 2

- [ ] 3.1 `openspec validate fix-ctrlg-coordinator-own-need --strict`
- [ ] 3.2 `make pre-pr` (full CI-mirroring gate) — must pass clean before opening/updating the PR
- [ ] 3.3 Archive within the same PR before merge: `openspec archive fix-ctrlg-coordinator-own-need` (merges the `hera-view` deltas into `openspec/specs/hera-view/spec.md` and moves the change folder to `openspec/changes/archive/<date>-fix-ctrlg-coordinator-own-need/`), commit the result on the change branch
