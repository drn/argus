## 1. Data layer: resolve each candidate's originating orchestrator

- [x] 1.1 Add `db.StuckTaskCandidate` (task + `Orchestrator` name) and change
  `StuckTaskCandidates` to return `[]*StuckTaskCandidate`, resolving the most
  recent orchestrator per task via a correlated subquery over
  `hera_bindings`/`hera_roles`/`hera_orchestrators` ordered by
  `started_at DESC`.
- [x] 1.2 Add a supporting index on `hera_bindings(argus_task_id, started_at)`
  (non-partial — the query deliberately reaches ended/archived/nuked rows).
- [x] 1.3 Tests: empty orchestrator for a non-Hera task; resolves through an
  ended+archived+nuked role/orchestrator; picks the most recent of two
  historical bindings across different orchestrators.

## 2. REST layer

- [x] 2.1 Add `orchestrator` (omitempty) to `cleanupCandidateJSON` in
  `internal/api/cleanup_candidates.go`; update the three
  `StuckTaskCandidates` call sites for the new return type.
- [x] 2.2 Test: `GET /api/maintenance/cleanup-candidates` includes the
  orchestrator name for a Hera-sourced candidate and omits it for a plain one.

## 3. TUI layer: tree rendering

- [x] 3.1 Add `Coordinator` to `mergeSafetyCandidate`; mirror the wire
  `orchestrator` field into it in `cleanupCandidatesToRows`
  (`internal/tui/mergesafety.go`).
- [x] 3.2 `MergeSafetyPopup.SetCandidates` groups each section's candidates by
  `Coordinator` (ungrouped first, flat; then one group header per distinct
  coordinator, in first-appearance order, followed by its own candidates).
- [x] 3.3 `drawRows` renders a coordinator group header (icon + name,
  `theme.IconCoordinator`/`theme.StyleCoordinator`) and indents its nested
  candidates one level deeper than a flat item.
- [x] 3.4 Tests: grouped rendering, ungrouped-stays-flat regression guard,
  mixed grouped+ungrouped in one section, multiple distinct coordinator
  groups, NOT-SAFE-before-SAFE ordering preserved across groups, one header
  per coordinator (not one per candidate). Existing scroll/section tests
  (all-ungrouped fixtures) verified unchanged.

## 4. Docs

- [x] 4.1 Update `context/knowledge/gotchas/hera-view.md` with the new
  orchestrator-resolution invariant.
- [x] 4.2 Archive this change into the base `hera-view` spec in the same PR.
