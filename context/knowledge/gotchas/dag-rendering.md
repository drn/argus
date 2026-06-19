# DAG Widget Rendering Gotchas

The `dagview` widget (`internal/tui/dagview/`) renders a layered top-down graph. As of the plan-view work it is a **layout library**, not an embedded widget: its sole consumer is `internal/tui/planview`, which calls `dagview.Compute` for stage (longest-path layer) placement of plan nodes. The standalone `dagview.Widget` is no longer mounted in any view — the **plan-DAG widget** (`planview`) replaced the retired orchestration-tree graph in the Hera Details pane, and the retired `heraTreeNodes` tree projection (`internal/tui/hera/tree.go`) is deleted. There is no standalone DAG tab and no web/JS counterpart (the SPA's DAG view and `/api/dag` were removed).

## Layout (still the shared primitive)

- **Layer = longest path from any source.** Kahn topological sort with memoised DFS, layer = max(parent.layer)+1. Sources (no parents) land at layer 0. `planview.computeStages` feeds `dagview.Compute` with `DependsOn = blockers` (a node depends on its `hera_blocks` blockers), so a plan node's stage is the computed longest path — independent of the short-id number in its name.
- **Within-layer ordering is barycentric, two sweeps.** Each node's column score = mean of its parents' columns; nodes are then sorted by score. Two sweeps converge well at Argus scale (≤30 nodes).
- **Sort by ID inside a layer is the determinism anchor.** Map iteration is randomised in Go; without the explicit stable sort, golden render tests would flake.

## Rendering

- **Iterate over `[]rune`, not the raw string, when placing a node label.** `for i, r := range s` returns byte indices; multi-byte glyphs like `✓`/`✕`/`○` (3 bytes) skip cells and leave gaps. Both `dagview` and `planview` use rune slices for chip text.
- **Edges drawn with single-line box chars only.** Mixing single (`─ │`) with double/thick creates joiner glitches in tcell.
- **Box width is constant in runes, not bytes.** Truncation is rune-count based.

## planview node colour + state (the new consumer)

- **Node colour comes from the bound task's argus status / result, NOT the hera role status.** `heraPlanNodes`/`heraPlanNodesWithBridge` stamp `planview.Node.State` from `RoleView.TaskStatus` + `RoleView.TaskResult` for a live node, and `StatePlanned` (violet `○`) for a never-bound planned role. A `{"failed":true}` result wins over the workflow status (red `✕`) via the shared `coordTaskFailed`. The rail's own status icons use the hera role status — the two are deliberately different signals.

## Widget integration

- **`planview.Widget` surfaces `OnEnter`, `OnDrillIn`, `OnDrillOut`, `OnBranchChange`.** `OnEnter` (Enter on a plain leaf) jumps to the node's agent view — the App wires it. `OnDrillIn` (Enter on a sub-coordinator) pushes the child orchestrator's plan — the **page** wires it (it needs the rail bridge index + the in-package projection; planview cannot import hera). There are no link/unlink/halt edit callbacks — plans are authored over MCP (`hera_plan*`), the view is read-only navigation.
- **`OnBranchChange` must be wired to `forceRedraw` (log-only, never `Sync`).** The snapshot install, focus flip, cursor move, group fan-out, and drill-in each shift the cell set in the same rect — tcell's per-cell diff leaves ghosts otherwise. Stale cells are prevented by `tview.Clear()` + the widget's full-rect `DrawBorderedPanel`/`FillArea`. See `gotchas/ui-threading.md`. `planview.branchShape` folds the (stage, slot, member) cursor, fanned-group state, and the current-orchestrator title hash into one uint64 so back-to-back `SetData` with the same shape doesn't spam `forceRedraw`.
