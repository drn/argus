# DAG Widget Rendering Gotchas

The `dagview` widget (`internal/tui/dagview/`) renders a layered top-down graph. As of the depends_on retirement it has ONE consumer: the Hera Details pane, where it renders the **orchestration tree** (the role hierarchy — coordinator → workers → sub-coordinators), projected by `heraTreeNodes` (`internal/tui/hera/tree.go`). It no longer renders `depends_on` edges, there is no standalone DAG tab, and there is no web/JS counterpart (the SPA's DAG view and `/api/dag` were removed). A tree is just a DAG, so the Sugiyama layout below is unchanged — only the node/edge source changed.

## Layout

- **Layer = longest path from any source.** Kahn topological sort with memoised DFS, layer = max(parent.layer)+1. Sources (no parents) land at layer 0. The coordinator (tree root) has no parent edge, so it lands at layer 0; its workers at layer 1; a sub-coordinator's workers at layer 2; and so on.
- **Within-layer ordering is barycentric, two sweeps.** Each node's column score = mean of its parents' columns; nodes are then sorted by score. Two sweeps converge well at Argus scale (≤30 nodes).
- **Parentless nodes within a layer sort _after_ parented nodes, by ID.** Prevents jitter when an orphan shares a layer with chained children.
- **Stale parent IDs are silently dropped from the edge list.** `heraTreeNodes` only emits edges to in-subtree task IDs, so this is belt-and-suspenders.
- **Sort by ID inside a layer is the determinism anchor.** Map iteration is randomised in Go; without the explicit stable sort, golden render tests would flake.

## Rendering

- **Iterate over `[]rune`, not the raw string, when placing the node label.** `for i, r := range s` returns byte indices; multi-byte glyphs like `✓`/`✕` (3 bytes) skip cells and leave gaps. Both `Draw` and `RenderToString` use rune slices.
- **Edges drawn with single-line box chars only.** Mixing single (`╭ ╮ ╰ ╯ ─ │`) with double/thick creates joiner glitches in tcell. The failed-result node gets a bold colour + `✕` glyph, not a thicker border.
- **Bent edges use corner glyphs on the same row, not a multi-row L-bend.** The child's top-border cells at the entering column remain `─` (documented cosmetic limitation).
- **Box width is constant in runes, not bytes.** `makeLabel` truncates by rune count and appends `…`.
- **Node colour comes from the bound task's argus status, not the hera role status.** `heraTreeNodes` stamps `Node.Status` from `RoleView.TaskStatus` (`in_progress`/`complete`/…) and `Node.Result` from `RoleView.TaskResult`, so the widget's status palette + failed-glyph (`✕` on `{"failed":true}`) work exactly as before. The rail's own status icons use the hera role status — the two are deliberately different signals.

## Widget integration

- **`dagview.Widget` surfaces `OnEnter` + `OnBranchChange` only.** The `OnLink`/`OnUnlink`/`OnHalt` callbacks and the `l`/`L`/`h` key cases were removed with depends_on — there are no edges to edit. `OnEnter` (Enter key) jumps to the node's agent view; the App wires it.
- **`dagview.Widget` must surface `OnBranchChange` and the App must wire it to `forceRedraw`.** The snapshot install, focus flip, and cursor move each shift the cell set in the same rect — tcell's per-cell diff leaves ghosts otherwise. `forceRedraw` is log-only (never `Sync`); stale cells are prevented by `tview.Clear()` + the widget's full-rect `DrawBorderedPanel`. See `gotchas/ui-threading.md`.
- **`branchShape` collapses many state bits to one uint64 signature** so back-to-back `SetNodes` with the same node/status set doesn't spam `forceRedraw`.

## Embedding in the Hera Details pane

- **The widget is reused as a sub-pane — retitle it via `SetTitle(" Orchestration Tree ")`.** `New()` defaults the title to " DAG "; the Hera page retitles it on construction so it doesn't read as a second top-level tab. Pass `""` to suppress the title text (border still draws).
- **`OnBranchChange` stays log-only.** Same cursor-highlight ghost-prevention contract as everywhere else.
- **The node set is the orchestration SUBTREE, projected by `heraTreeNodes(rail.Model(), sel.Orch)`** (`internal/tui/hera/tree.go`), NOT a provider seam and NOT a `depends_on` graph. It is a pure in-memory read over the rail's already-built `Model` (no DB call). The subtree is discovered by multi-binding BFS in memory: orchestrator C is a child of P when C's live coordinator task is bound as a (non-coordinator) worker task under P. Workers get a SYNTHETIC edge to their orchestrator's coordinator; a bridge task (worker-in-parent + coordinator-in-child) collapses to ONE node keyed by task ID, so it carries both a parent edge and child edges — that is what stitches the subtrees together. The coordinator never gets a self-edge (cycle-safe). See `gotchas/hera-view.md`.
