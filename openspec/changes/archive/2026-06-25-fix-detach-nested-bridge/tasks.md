## 1. Selection resolution (`internal/tui/heraactions.go`)

- [x] 1.1 In `heraCoordReparentTarget`, before the worker-row early `return
  false`, add a branch: when `sel.Role` is a NON-archived `worker`-kind role AND
  `sel.BridgeChildOrchID != 0`, return `(sel.BridgeChildOrchID, sel.Role.Name,
  roleReclaimTask(sel.Role), true)` — the bridge row IS the child's coordinator,
  so it routes the coordinator detach/re-parent path. A plain worker
  (`BridgeChildOrchID == 0`) still returns false.
- [x] 1.2 Update the helper's doc comment to name the third qualifying shape (a
  worker-bridge sub-coordinator row), and note the guard against misclassifying a
  plain worker.

## 2. Tests (`internal/tui/heraactions_test.go`)

- [x] 2.1 `TestHeraCoordReparentTarget` — add subtests: a non-archived
  worker-bridge row (`BridgeChildOrchID` set) qualifies and returns the CHILD
  orch id + the bridge role's name + its bridge task; a plain worker
  (`BridgeChildOrchID == 0`) does NOT qualify; an ARCHIVED worker-bridge row does
  NOT qualify.
- [x] 2.2 Smoke test (`TestSmoke_HeraDetachNestedBridgeThroughPicker`): seed
  child nested under parent (via `ReparentCoordinator`), select the bridge row
  shape (worker role + `BridgeChildOrchID = child`), press `J` → picker opens →
  Enter on the detach sentinel → the parent link is gone (child top-level again),
  child's own coord role + binding intact.
- [x] 2.3 Smoke test (`TestSmoke_HeraReparentNestedBridgeThroughPicker`):
  selecting the bridge row + picking a DIFFERENT parent re-parents the nested
  coordinator (symmetry).
- [x] 2.4 Confirm existing coordinator-header / coordinator-role detach +
  re-parent tests still pass (no regression).

## 3. Docs

- [x] 3.1 Add the invariant to `context/knowledge/gotchas/hera-view.md` (1–2
  sentences): a nested sub-coordinator renders as a headerless worker-bridge row,
  so `J` must resolve the child orchestrator via `Selection.BridgeChildOrchID`
  (the SAME field `Ctrl+D` uses) to detach/re-parent it.

## 4. Archive

- [x] 4.1 Merge the delta into `openspec/specs/hera-view/spec.md` and move this
  change folder to `openspec/changes/archive/<YYYY-MM-DD>-fix-detach-nested-bridge/`
  (manual merge-and-move; `openspec` CLI may be absent).
