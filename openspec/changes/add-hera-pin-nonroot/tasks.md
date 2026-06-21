# Tasks: Pin non-root Hera rail items, rendered with a lineage breadcrumb

**Design doc:** `openspec/changes/add-hera-pin-nonroot/design.md`

## 1. Tests (Prove-It — red first)

- [x] 1.1 Model: `TestBuildModel_RoleViewPinned` — a role with `pinned_at` set → `RoleView.Pinned == true`; NULL → false.
- [x] 1.2 Rail: `TestRail_PinnedLeafFloatsAsBreadcrumb` — a pinned leaf worker renders a selectable `rrPinnedBreadcrumb` line + a non-selectable continuation name line, and does NOT also render under its orchestrator in the active tree (single placement).
- [x] 1.3 Rail: `TestRail_PinnedBreadcrumbLineageIsCanonicalChain` — breadcrumb trail equals the `canonicalParents` chain `root › sub ›` for a role under a nested sub-coordinator.
- [x] 1.4 Rail: `TestRail_PinnedBreadcrumbLeftTruncates` — an over-wide trail is left-truncated with a leading `…`, nearest parent visible (assert via the draw path / `truncRunesLeft`).
- [x] 1.5 Rail: `TestRail_PinnedRoleUnderPinnedOrchStaysNested` — a pinned role whose orchestrator is also pinned does NOT float (renders nested under the pinned root).
- [x] 1.6 Rail: `TestRail_PinnedSubCoordHoistsSubtree` — a pinned bridging sub-coordinator floats with its child subtree rendered beneath it; child rendered exactly once (not in the active tree), in BOTH collapsed and expanded fold states.
- [x] 1.7 Rail: `TestRail_PinnedHeaderShowsForRoleOnly` — the "Pinned" header renders when only a non-root role is pinned (no pinned orchestrator).
- [x] 1.8 Rail: `TestRail_PinnedBreadcrumbCursorAnchorAndSkip` — `j`/`k` lands on the breadcrumb line and skips the continuation line; `currentRef`/`restoreCursor` re-pin by role id after a rebuild.
- [x] 1.9 Rail: `TestRail_PinnedRoleFilterStates` — under an active `/` filter a non-matching floated pinned role is omitted and the Pinned header prunes when empty; a matching one stays.
- [x] 1.10 Rail: `TestRail_PinnedRoleUnresolvableParentNotFloated` — a pinned role with an unresolvable orchestrator is skipped from the Pinned block (not rendered without lineage).
- [x] 1.11 SimulationScreen render: `TestRail_PinnedBreadcrumbDrawsAndLeftTruncates` — Draw a model with a pinned non-root role on a real `tcell.SimulationScreen`, assert the breadcrumb trail + name render and the trail left-truncates (satisfies the CLAUDE.md "rail render change → SimulationScreen test" rule; the `P` InputHandler routing is unchanged, so no app-level event-loop smoke is added — the pin→float chain is covered by `ops_test` PinToggle-role + `TestBuildModel_RoleViewPinned` + the rail tests).
- [x] 1.12 Confirm each `it should X` acceptance criterion in `design.md` maps to a failing test above (Prove-It).

## 2. Read-model projection

**Depends on:** Stage 1

- [x] 2.1 Add `RoleView.Pinned bool` to `internal/tui/hera/model.go` with a doc comment (pinned_at projection; feeds the rail float decision).
- [x] 2.2 Set `rv.Pinned = role.PinnedAt != nil` in `buildRoleView`.
- [x] 2.3 Make tests 1.1 pass.

## 3. Two-line pinned breadcrumb render (leaf roles)

**Depends on:** Stage 2

- [x] 3.1 Add row kind `rrPinnedBreadcrumb` and fields `breadcrumb string` + `breadcrumbCont bool` to `railRow` (rail.go).
- [x] 3.2 `selectable()`: `rrPinnedBreadcrumb` → true; `rrRole` with `breadcrumbCont` → false.
- [x] 3.3 `currentRef`/`restoreCursor`/`SelectByTaskID`: map `rrPinnedBreadcrumb` to `role.RoleID`; exclude the `breadcrumbCont` continuation so it never shadows the anchor.
- [x] 3.4 Add `Rail.collectPinnedRoles()` — walk `r.model`, collect floated pinned roles (`RoleView.Pinned` && containing orch not pinned && orch resolvable), compute the breadcrumb via `canonicalParents` + `OrchByID(...).Name` chain; return the floated roles + a `pinnedFloat` set of role ids. Apply the active filter (skip non-matching). `uxlog` the floated count + any unresolvable-parent skips.
- [x] 3.5 In `buildRows` Pinned section: after pinned orchestrators, emit each floated pinned role as a `rrPinnedBreadcrumb` row (with `breadcrumb`) + a `rrRole` continuation row (`breadcrumbCont`, depth+1). Render the "Pinned" header when `len(Model.Pinned) > 0 || len(floated) > 0`.
- [x] 3.6 In `appendOrchWorkers`/`appendWorkerRow`: skip a worker row whose role id is in `pinnedFloat`.
- [x] 3.7 Draw: add `drawPinnedBreadcrumbRow` (dimmed icon + `truncRunesLeft` ancestry) and a continuation-name draw (name selected-style when the preceding breadcrumb is the cursor — Draw loop passes `selected || (breadcrumbCont && idx-1 == cursor)`). Add a rune-aware `truncRunesLeft` helper (or reuse an existing one).
- [x] 3.8 Make tests 1.2–1.5, 1.7–1.10 pass.

## 4. Pinned sub-coordinator subtree hoist

**Depends on:** Stage 3

- [x] 4.1 In `collectPinnedRoles`/`buildRows`: when a floated pinned role bridges a child orchestrator (`workerBridgeChild` / `collOrchID`), set the breadcrumb row's `collOrchID = child.ID` and, after the name line, render the child subtree via `appendOrchWorkers(child, …)`, marking `placed[child.ID] = true`.
- [x] 4.2 Verify the active-tree passes + `structuralReach` safety sweep leave the hoisted child folded (it is already `placed`); add no leak. Respect `isCollapsed(child.ID)` for the fold.
- [x] 4.3 Confirm `Selection()` carries `BridgeChildOrchID` for the pinned sub-coord breadcrumb row (Ctrl+D cascade + Left parent-nav parity) — it already keys on `row.role != nil && row.collOrchID > 0`.
- [x] 4.4 Make test 1.6 pass; make smoke test 1.11 pass.

## 5. Docs + gotchas

**Depends on:** Stage 4

- [x] 5.1 Add gotcha bullets to `context/knowledge/gotchas/hera-view.md`: two-line pinned entry (selectable line-1 / non-selectable continuation line-2, cursor anchors on line 1); breadcrumb = `canonicalParents` chain (consistency with nesting); single-placement via `pinnedFloat` + sub-coord hoist via `placed` (Pinned renders before active); pin is DB-backed (no `railViewState` change); orphan = skip+log.
- [x] 5.2 Confirm help modal + README need NO change (`P` already bound + documented "toggle pin"; no key added/rebound). Note the rationale in the commit.

## 6. Gate

**Depends on:** Stage 5

- [x] 6.1 Run targeted tests: `go test ./internal/tui/hera/... ./internal/db/...` (use `GIT_CONFIG_GLOBAL=/dev/null` if local git-signing flake hits `internal/agent`). Format with `make fmt`.
- [x] 6.2 Report DONE to the coordinator (`hera_send`) with branch name + a 1-line summary. (Coordinator runs full `make pre-pr` after merge.)
