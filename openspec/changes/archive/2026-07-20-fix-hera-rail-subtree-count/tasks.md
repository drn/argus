## 1. Fix the rail header count

- [x] 1.1 `Model.SubtreeAgentCount(orchID int64) int` (`internal/tui/hera/model.go`,
  next to `BridgeSubtree`): walks `BridgeSubtree(orchID)` and counts every
  `Kind == db.HeraKindWorker` role across every orchestrator in the subtree —
  archived roles included, `Live` ignored. Coordinator roles excluded at
  every level (folded into their own header / already represented by the
  parent's bridging worker row).
- [x] 1.2 `internal/tui/hera/rail.go`'s `drawOrchRow` calls
  `r.model.SubtreeAgentCount(o.ID)` instead of the old `liveRoleCount(o)`.
- [x] 1.3 Delete `liveRoleCount` — fully superseded, no remaining callers.

## 2. Tests

- [x] 2.1 `TestModel_SubtreeAgentCount_IncludesArchivedRegardlessOfLiveness`
  (`internal/tui/hera/model_test.go`): an orchestrator with one live worker
  and two archived workers (one with `Live: true` — the un-torn-down case,
  one with `Live: false`) counts all 3, excluding the coordinator.
- [x] 2.2 `TestModel_SubtreeAgentCount_RecursesIntoNestedSubcoordAndItsArchive`:
  a root orchestrator bridging to a child sub-coordinator counts the child's
  own workers (including its archived ones) without double-counting the
  bridging row against the child's own coordinator role.
- [x] 2.3 `internal/tui/hera/rail_test.go` and
  `internal/tui/hera/bug024_test.go`'s existing `liveRoleCount` assertions
  updated to `Model.SubtreeAgentCount` (same expected values — neither
  fixture has archived or nested roles, so the numbers are unchanged; only
  the call site moves).

## 3. Spec

- [x] 3.1 `hera-view` delta: "Orchestrator and role row rendering" requirement
  text + scenario updated to describe the subtree/archive-inclusive count.
