# DAG Rendering Gotchas

The TUI DAG widget (`internal/tui/dagview/`) and its web counterpart (`internal/api/static/index.html` `loadDAG` / `layoutDAG` / `renderDAG`) implement the same Sugiyama-lite layout twice — once in Go, once in JS — because shipping the layout server-side would force a second round-trip per render. Keep these invariants in mind when touching either copy.

## Layout

- **Layer = longest path from any source.** Kahn topological sort with memoised DFS, layer = max(parent.layer)+1. Sources (no parents) land at layer 0. Diverging from "longest path" loses the visual that "everything in column N happens after everything in column N-1".
- **Within-layer ordering is barycentric, two sweeps.** Each node's column score = mean of its parents' columns; nodes are then sorted by score. Two sweeps converge well at Argus scale (≤30 nodes). Adding more sweeps changes nothing visible but burns CPU; fewer increases edge crossings.
- **Parentless nodes within a layer sort _after_ parented nodes, by ID.** Without this, a layer with one orphan among five chained children jitters the layout on each refresh because the orphan's barycenter is +Inf.
- **Stale parent IDs are silently dropped from the edge list.** A `depends_on` referencing a deleted task does NOT crash the layout — the daemon's halt cascade or a manual deletion can leave dangling refs and the DAG view still renders the partial graph.
- **Sort by ID inside a layer is the determinism anchor.** Map iteration is randomised in Go, so without the explicit stable sort, two layouts of the same input would produce different orderings and golden render tests would flake.

## Rendering

- **Iterate over `[]rune`, not the raw string, when placing the node label.** `for i, r := range s` returns byte indices; multi-byte glyphs like `✓` (3 bytes) and `✕` (3 bytes) skip cells and leave gaps. Both `Draw` and `RenderToString` use rune slices for this reason.
- **Edges drawn with single-line box chars only.** Mixing single (`╭ ╮ ╰ ╯ ─ │`) with double or thick (`╔ ╗ ╚ ╝ ═ ║`) creates joiner glitches in tcell. The renderer uses single-line throughout; the failed-result node gets a bold colour + `✕` glyph, not a thicker border.
- **Bent edges use corner glyphs on the same row, not a multi-row L-bend.** The corners (`╰─...─╮` going right, `╭─...─╯` going left) visually bridge into the child's top border. The top-border cells of the child at the entering column remain `─`, which is a documented cosmetic limitation — the corner glyphs do not perfectly join the horizontal. Fixing this would require post-processing the child's border to swap `─` for `┬` at incoming-edge columns; deferred until users complain.
- **Box width is constant in runes, not bytes.** `makeLabel` truncates by rune count and appends `…` so multi-byte glyphs don't push the box past `boxWidth`.

## Web parity

- **The JS `layoutDAG` mirrors `dagview.Compute` byte-for-byte semantically.** Any change to the Go algorithm must update the JS, and vice versa. Both are small (~60 lines each); a future refactor could move layout server-side and return positions, but the current two-implementations setup avoids a second round-trip on every refresh.
- **Web `SW_VERSION` bumps are mandatory on any static asset change** (HTML, manifest, vendor JS/CSS). The service worker serves the shell cache-first — without bumping `SW_VERSION` in `internal/api/static/sw.js`, every installed PWA keeps serving the stale shell forever.

## Widget integration

- **`refreshDAG` filters archived rows and pure orphans before `SetNodes`.** A pure orphan = no _live_ parents (no DependsOn id resolving to a non-archived task) AND not referenced as a parent by any surviving (non-archived) task. A task with only stale/archived DependsOn ids counts as having no live parents. Without this, every standalone task piles up at layer 0 and pushes the connected graph off-screen — visible in the original "DAG view doesn't render" bug. The filter is in `dagNodesFromTasks`; it's an unconditional TUI default, not a toggle. The web's `/api/dag` has separate filter knobs (project, plan, archived) — keep the two surfaces in step when adding new filter dimensions.
- **`dagview.Widget` must surface `OnBranchChange` and the App must wire it to `forceRedraw`.** The DAG snapshot install, focus flip, and cursor move each shift the cell set in the same rect — tcell's per-cell diff leaves ghosts otherwise. See `gotchas/ui-threading.md` and CLAUDE.md's "branch-change callback contract".
- **`branchShape` collapses many state bits to one uint64 signature** so back-to-back `SetNodes` with the same node set / status set doesn't spam `forceRedraw`. The bits cover node count, layer count, edge count, focus, failed-result count, cursor-present, and in-progress count — enough to catch every animation-relevant transition.
- **DAGPage.MouseHandler always redirects focus to the inner widget on click.** The default `tview.Box.MouseHandler` steals focus to the page wrapper, which has no `InputHandler` — all keyboard input then drops. Pattern matches `SettingsPage` and `TaskPage`. Required for every new page wrapper.

## Embedding (M7 — DAG folded into the Hera Details pane)

- **`dagview.Widget` is reusable as a sub-pane as-is — but retitle it via `SetTitle`.** The widget hardcodes a " DAG " bordered-panel title; `New()` defaults it, and `SetTitle(" Dependencies ")` retitles it when embedded so it doesn't read as a second top-level " DAG " tab. Pass `""` to suppress the title text (border still draws). The standalone DAG tab keeps the default. The Hera `HeraPage` owns the embedded widget's rect/focus/node set; the App wires its callbacks (`DAG()` accessor), exactly mirroring the legacy tab.
- **`OnBranchChange` stays log-only in BOTH surfaces.** The App wires the embedded widget's `OnBranchChange` to `forceRedraw` (never `Sync`) — same cursor-highlight ghost-prevention contract as the standalone tab. The widget covers its full sub-rect (its own `DrawBorderedPanel`), so no stale cells survive without Sync.
- **The embedded DAG's node set is orchestrator-SCOPED, projected via a seam.** `HeraPage.SetDAGNodeProvider(func(*OrchView) []dagview.Node)` is wired by the App to `dagNodesFromTasks(scopeTasksToOrch(db.Tasks(), orch))`. `scopeTasksToOrch` filters the full task list to the orchestrator's LIVE-bound task IDs (its roles' `TaskID`s), then the SHARED `dagNodesFromTasks` applies the archived-drop + orphan-filter — **do NOT fork the projection** into the hera package (web/TUI parity + no Tasks() seam on `HeraReader`). The DAG shows depends_on edges among THIS orchestrator's tasks; the rail shows the orchestrator tree (tree ⊥ DAG — orthogonal, per the plan).

## Link / unlink pickers (M7 — implemented; no longer stubs)

- **`openLinkPickerForTask` / `openUnlinkPickerForTask` (`internal/tui/dagactions.go`) are now REAL pickers** shared by both the embedded Hera DAG and the legacy DAG tab (same widget callbacks). Link offers every non-archived task except the child + its existing parents (candidates are NOT orchestrator-scoped — a depends_on link is a global relationship, even though the DAG VIEW is scoped); unlink offers the child's current parents (including stale-ref parents so dangling edges can be cleaned up). A child with no parents surfaces a notice instead of an empty picker.
- **Cycle rejection is `orch.Link`'s job, not the caller's.** `doLink` (`herapicker.go`) calls `orch.Link`, which stages the edge and runs `FindCycle` internally, returning `*orch.CycleError` with the graph unchanged. `doLink` type-asserts via `errors.As` and surfaces "link rejected: would create a dependency cycle" — the link is NOT created. `orch.Unlink` cannot induce a cycle. After a successful link/unlink, `afterDependencyChange` refreshes BOTH DAG surfaces (`refreshDAG` + `heraPage.Refresh`) + the task list.
- **The picker reuses `TaskSwitcherModal`, generalized with `SetTitles(title, help)`.** New `modeHeraPicker` + early dispatch (before the rune switch, so 1/2/3/q don't leak); the close path returns to the "hera" page. The generalization mirrors M6c's `NewInputForm(title)` move.
- **`TaskSwitcherModal`'s title loop now iterates `[]rune`, not the byte-indexed `range` over the string.** A multi-byte title rune (the link picker's `→`) would otherwise leave gap cells — the same rune-vs-byte placement bug documented above. ASCII titles were unaffected, which is why it survived until M7.

## TUI follow-ups (known limitations)

- **The `h` halt-downstream keybinding has no confirm modal.** Calls `orch.HaltDownstream` directly (the embedded Hera DAG also refreshes via `heraPage.Refresh()` after a halt). Destructive but reversible (archived rows unarchive, stopped tasks resume), so the trade-off favours one-key access until usage surfaces a foot-gun.
