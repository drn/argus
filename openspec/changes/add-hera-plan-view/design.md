## Context

The `add-hera-plan-substrate` change (now Complete, pending push/archive) gave hera a durable order-of-ops graph: a *planned node* is a `hera_roles` row with no binding; `hera_blocks(blocked_role_id, blocker_role_id)` is the edge table; the gater materializes a planned node into a live worker once all blockers reach hera role-status `done`. The substrate deliberately deferred the **rendering** — its non-goals say "the tree rendering (short-id layout, collapsing groups, navigation) — follow-up view change." This is that follow-up.

Today the Hera Details pane (`internal/tui/hera/details.go`) renders, for a selected coordinator, a roster stacked over an embedded `dagview` graph. That graph is the **orchestration tree** (the role hierarchy coordinator→workers→sub-coords), projected in-memory by `heraTreeNodes` (`internal/tui/hera/tree.go`). It does **not** show `hera_blocks` edges or planned nodes — so the plan a coordinator authored is invisible in the TUI; the only way to see it is the node-state SQL.

This change re-points that embedded graph at the plan DAG: the blocking edges plus planned nodes, rendered as the "tight tree" UX locked in the design artifact (https://claude.ai/code/artifact/a9fe7589-7af5-4c69-b911-2396a84cb1e1). The artifact is the authoritative visual + interaction spec; this doc records the decisions needed to realize it in tcell/tview against the real hera store.

**Constraints inherited from the codebase:**

- TUI-only. The SPA DAG view and `/api/dag` were retired with `depends_on`; there is no web counterpart and this change does not add one.
- No `screen.Sync()` for content updates (CLAUDE.md UX-rendering rules). Stale cells are prevented by `tview.Clear()` every frame plus full-rect `DrawBorderedPanel`. The branch-change callback stays log-only.
- `BuildModel` is a pure read on the tview thread (no Draw-time I/O); the projection must stay pure over the already-built `Model`.
- Remote mode passes a nil `HeraReader`; the plan view must degrade with the existing "Hera unavailable" path, never panic.

## Goals / Non-Goals

**Goals:**

- Render the orchestrator's plan DAG (planned + live roles, `hera_blocks` edges) in the Details pane, replacing the orchestration-tree graph.
- Short-id labels parsed from the role-name prefix (`2c-fact-checker` → display `2c`), number=stage, letter=parallel member.
- Auto-collapse parallel groups into a range box (`[2a–2c]`) with aggregate state counts; non-contiguous ids render `[2a–2f +2]`.
- 4-way keyboard navigation: `↑↓` change stage, `←→` move within a stage, `Enter`/`Space` fan a group out / collapse it; inside a group `←→` walks members and stepping off the edge exits+collapses.
- A master-detail header above the diagram showing the selected node (name / description / feeds) or selected group (range / members / downstream).
- Partial-dependency rendering via **option B**: a partially-feeding group carries a `↘` marker; the feeding member carries a `↘` chip; full membership truth shows on fan-out.
- Sub-coordinator drill-in: `Enter` on a node that is a sub-coordinator swaps the diagram to that child orchestrator's plan DAG; `Esc` pops back to the parent.
- State palette + glyphs matching the artifact: done `✓` green, working `⟳` amber, in_review `◔` cyan, planned `○` violet, failed `✕` red.

**Non-Goals:**

- A human plan editor. Plans are authored over MCP (`hera_plan*`); the view is read-only navigation. No add/remove-node or add/remove-edge keys.
- Boundary-piercing cross-orchestrator edges (the substrate's non-goal stands). Drill-in *navigates* between per-orchestrator DAGs; it does not draw an edge across the boundary.
- Reviving any retired surface (`/api/dag`, standalone DAG tab, SPA DAG view).
- Re-laying-out the rail, the coordinator/worker PTY panes, or the roster. Only the embedded graph (and the new header strip above it) change.
- Mutating plan state from the view (no gater triggering, no status changes from the graph).

## Decisions

### D1 — Replace the tree graph with the plan graph (not a toggle)

The Details pane's embedded graph becomes the plan DAG unconditionally; the orchestration-tree projection (`heraTreeNodes`, `tree.go`, `tree_test.go`) is retired. Chosen over a tree↔plan toggle key (one fewer key to discover/document; the plan graph subsumes the tree's "what work exists" question and adds ordering) and over keeping both modes (two nav models in one surface).

**Chesterton's Fence — the tree is not pure loss.** The tree's value was "see every role and the hierarchy." The plan view emits the **same role set** (planned + live), so no role goes missing. What's dropped is the explicit coordinator→worker *hierarchy edges*; the sub-coordinator relationship is preserved as drill-in (D6) rather than as in-graph nesting. The roster (the textual `Agents (N):` list in `DetailsView`) already lists every worker, so the per-role inventory survives independent of the graph.

**Degenerate "no plan authored" case (the common case — the gater is dark until a plan exists):** the graph still renders the orchestrator's live roles as a **flat, edgeless single stage**, titled with a "no plan authored" hint. Running workers are never invisible. Only an orchestrator with zero non-coordinator roles renders the empty placeholder. This makes "Replace" safe to ship before any orchestrator has a plan.

### D2 — A new `planview` widget, reusing `dagview`'s layer math

Build a dedicated plan-view widget rather than bolting a "plan mode" onto `dagview.Widget`. The tight-tree interaction model — a cursor over `(stage, slot, member)` with group collapse/fan-out and a navigation stack — is structurally different from `dagview`'s grid cursor (`MoveCursor(dx,dy)` over `layer/col`), and the master-detail header is a new region the tree never had. A "mode" enum would fork layout, nav, draw, and the cursor type throughout one widget. A separate widget keeps each surface independently testable (the CLAUDE.md rule for conditional-branch widgets) and avoids a fat conditional.

**Reuse, not rewrite:** `planview` imports `dagview`'s layer-assignment layout (Kahn longest-path + barycentric within-layer ordering) to assign each node a stage row, and reuses the bordered-panel + branch-change + theme helpers. `dagview` therefore survives as a layout/render library; only its role as the Details-pane *tree* renderer is replaced.

*Alternative considered — extend `dagview` with a plan mode:* fewer new files, but every method gains a `if mode == plan` fork and the group/member cursor cannot share the grid cursor. Rejected for testability and clarity.

Open: package location — `internal/tui/planview` (sibling to `dagview`) vs a type inside `internal/tui/hera`. Leaning sibling package so the layout reuse is a clean import and the widget is unit-testable without the rail. (Flagged in Open Questions.)

### D3 — Layer = computed longest-path from edges; short-id is label-only

Stage placement comes from the `hera_blocks` graph (longest path from any source, via `dagview`'s existing Kahn layering), **not** from the short-id's number. The substrate explicitly accepts that "short-id assigned-number can drift from computed stage after plan edits" and names the baked-in short-id the stable *handle*, with row grouping a floating view concern. So the view computes truth from edges and uses the short-id purely as the display label. A node whose name has no parseable short-id prefix falls back to a truncated name label and still lays out by edges.

### D4 — Parallel-group definition and auto-collapse

A group is a maximal set of nodes in the same computed stage that **share the same blocker set and have no edges among themselves** (the artifact's live definition). Groups auto-collapse to a range box `[firstId–lastId]` (ids sorted); non-contiguous membership renders `[firstId–lastId +N]` (range of the span, count of the extras, no enumeration). The box shows aggregate counts by state (`3 ✓ · 2 ⟳ · 1 ○`). A stage with a single node, or nodes that don't form a clean group, render as individual chips. Grouping is a pure layout pass over the projected nodes+edges; it holds no state beyond which group (if any) is currently fanned out.

### D5 — Partial dependency = option B (marked member)

When only some members of a group feed a downstream node, the collapsed box carries a `↘` marker (`[2a–2c ↘]`) meaning "one member continues downstream," and on fan-out the specific feeding member carries a `↘` chip. The group stays whole when collapsed ("this blocks that"); the precise edge is revealed on fan-out. This is the artifact's recommended option and the prior locked call. (Option A — collapsed edge overstates the dep — and option C — hoist the distinct member out of the group — are recorded in the artifact and rejected there.)

### D6 — Sub-coordinator drill-in via an orchestrator nav stack

A node is a sub-coordinator when its bound task is the coordinator of a child orchestrator — already discoverable in-memory via the rail's bridge machinery (`CoordBridgeTaskID` / `bridgeIndex` / `workerTaskSet`). `Enter` on such a node pushes the child orchestrator onto a nav stack and re-projects the plan DAG for the child; `Esc` (or a `←`/back at the root) pops. The header's title reflects the current orchestrator (`Details ▸ <orch> · Plan`). This does **not** create a cross-orchestrator edge (the substrate non-goal holds) — it is navigation between two independently-projected per-orchestrator DAGs.

**`Enter` is overloaded by target:** on a parallel **group**, `Enter` fans out/collapses (D4); on a **sub-coordinator node**, `Enter` drills in (D6); on a plain leaf node, `Enter` keeps the existing `dagview` behavior (jump to that task's agent view). The three are disjoint by the cursor's target type, so there is no ambiguity, but it is documented in the help modal and the hint footer.

### D7 — Node color / glyph source

A **live** node colors from its bound argus task status + result (the existing `RoleView.TaskStatus`/`TaskResult` path that `heraTreeNodes` already used and `dagview` already palettes, including the `{"failed":true}` → red `✕`). A **planned** node (never bound) renders violet `○`. A node is "planned" when it is a worker-kind role with `Live=false` and no binding ever (`BridgeTaskID==""`); a finished/dead node (bound, binding ended) keeps its task-status color, not the planned glyph. This reuses the substrate's own planned-vs-materialized discriminator (`HeraRoleHasBinding`) projected to the UI.

### D8 — Data plumbing: one new edge query + a projection

`ListHeraRoles` already returns planned roles (they are just unbound worker roles), so the only missing data is the **edges**. Add:

- `db.ListHeraBlocks(orchID int64) ([]HeraBlock, error)` — all blocking edges whose endpoints are in the orchestrator (one query; the substrate only has the per-role `HeraBlockersOf`).
- A `HeraReader.ListHeraBlocks` method on the read seam; `*db.DB` satisfies it, remote mode's nil reader degrades as today.
- `BuildModel` reads edges once and attaches them to each `OrchView` (e.g. `OrchView.Blocks []db.HeraBlock`), and stamps a `RoleView.Planned bool` discriminator.
- A new pure projection `heraPlanNodes(orch *OrchView) ([]planview.Node, []planview.Edge)` (parallel to the retired `heraTreeNodes`) over the already-built `Model` — no DB read at Draw time.

### D9 — Master-detail header

A header strip renders above the diagram inside the same bordered panel (the artifact's `.detail` region): for a node, `Name` / `Description` / `Feeds`; for a collapsed group, `Group` (range · title) / `Members` / `Downstream`. "Description" is the role's delivery prompt's first line (the substrate persists the prompt on the planned role); "Feeds"/"Downstream" derive from the edge set. The header height is fixed and budgeted exactly (mirroring `DetailsView.ContentHeight`'s discipline) so the diagram gets the remainder without truncation drift.

**Render note — centered tight tree + cursor highlight.** Each stage row is centered horizontally within the diagram region (`inner.X + max(0, (inner.W - rowWidth)/2)`, where `rowWidth` is the rune-aware sum of chip widths plus inter-chip gaps) and the whole block is centered vertically when shorter than the region, matching the web artifact's centered tight tree rather than left/top-aligning at `inner.X`/`inner.Y`. The chip under the cursor is drawn with a reverse-video highlight (bold when the widget owns focus) so the selected node is visible in the diagram, not only in the header. Both are pure render concerns — no `Sync`, full-rect coverage preserved.

**Refresh invariant — cursor/fan-out preservation.** `applySelection` re-projects the selected coordinator's plan on every ~1s refresh tick. The widget exposes two install paths: `SetData` full-resets the cursor + fan-out (correct for a genuine selection change and the drill-in push/pop) and `UpdateData` preserves them (correct for the same-orchestrator refresh — no-op on an unchanged structural signature; re-anchor by node-id / group-member-set + clamp when the structure evolved). `rebuildPlan` tracks the currently-projected orchestrator id and calls `UpdateData` when it is unchanged, `SetData` otherwise. Without this split the tick clobbers the operator's cursor and collapses a fanned group ~1s after they act.

## Risks / Trade-offs

- **Retiring `heraTreeNodes` removes a shipped view** → mitigated by D1: the plan graph emits the same role set, the roster lists every worker, and the degenerate case renders live roles flat. Net loss is only the explicit hierarchy edges, replaced by drill-in.
- **Short-id parse failures or duplicate/missing prefixes** (a hand-spawned worker with no short-id, or two `2b`s after an edit) → the label falls back to the truncated name; layout is edge-driven so a missing/dup short-id never breaks placement, only the label nicety.
- **Group definition is heuristic** ("same blocker set, no internal edges") → a stage that doesn't form a clean group simply renders as individual chips; grouping is best-effort presentation, never affects the underlying edges or gater.
- **Drill-in into a deep sub-coord chain** → the nav stack is bounded by the bridge graph (cycle-safe, as `BridgeSubtree` already is); `Esc` always pops, and the root pop returns to the parent the rail selected.
- **Branch-change ghosting on group fan-out / drill-in** (the same class `dagview` documents) → the new widget honors the branch-change contract (signature folds in stage/slot/member cursor, fanned group, and current orchestrator) so cursor/expand shifts repaint the rect; still log-only, never `Sync`.
- **Header height + diagram budget drift on resize** → fixed header height with exact budgeting (D9), full-rect coverage, no `Sync` (CLAUDE.md).

## Migration Plan

- Additive on the data side (`ListHeraBlocks` is a new read; no schema change — `hera_blocks` already exists from the substrate). Retiring `tree.go` is a TUI-internal swap with no persistence impact.
- No flag: the plan view replaces the tree graph directly. The degenerate case (D1) makes this safe for every existing orchestrator with no authored plan.
- Rollback: revert the change; `dagview` + `heraTreeNodes` are restored by the revert. No data to migrate either direction.
- Depends on `add-hera-plan-substrate` being present (the `hera_blocks` table + planned roles). On this branch it is.

## Alternatives considered

- **Tree↔plan toggle key** (D1) — keeps both graphs, costs a key + a mode the user drives. Rejected: the plan view subsumes the tree's inventory question; the roster already lists roles.
- **Plan mode inside `dagview`** (D2) — fewer files, but forks every method and can't share a cursor type. Rejected for testability.
- **Stage from short-id number** (D3) — simpler, but drifts from the real edge graph after edits. Rejected: layout must reflect truth, label can drift.
- **Partial-dep option A / C** (D5) — A overstates the collapsed edge; C breaks the group apart. Rejected in the artifact in favor of B.
- **Defer sub-coord drill-in** — considered; the user scoped it into v1.

## Discovery findings

- **Planned roles are already in the Model.** `ListHeraRoles(orchID, true)` returns unbound worker roles; `BuildModel`/`buildRoleView` project them as `RoleView{Live:false, TaskID:"", BridgeTaskID:""}`. So only edges + a `Planned` discriminator are new — not a whole new role-loading path.
- **The edge query is the gap.** The substrate's `db/hera_plan.go` exposes `HeraBlockersOf(roleID)` (per-role) and `ListHeraPlannedNodes()` (all planned) but no orchestrator-scoped bulk edge fetch. `ListHeraBlocks(orchID)` is the one new query.
- **`dagview` is reusable as a layout library.** `Compute` does Kahn longest-path layering + barycentric ordering and is the right primitive for stage assignment; `widget.DrawBorderedPanel` + the branch-change pattern + the theme palette all transfer. Only the cursor model, grouping, header, and drill-in are net-new.
- **`dagview`'s only consumer is the Details pane.** Per `gotchas/dag-rendering.md`, there is no other caller and no web counterpart, so swapping the Details-pane graph is the whole blast radius on the render side.
- **The bridge machinery already finds sub-coordinators.** `CoordBridgeTaskID`/`bridgeIndex`/`workerTaskSet`/`BridgeSubtree` (in `model.go`) discover the worker→child-coordinator relationship in-memory and cycle-safe — drill-in reuses them, not a new traversal.
- **Failed-glyph parsing exists twice already** (`dagview.parseFailed`, `details.coordTaskFailed`) — the plan view should reuse one, not add a third.

## Acceptance criteria

**Projection + data**

- it should surface every blocking edge of an orchestrator to the view via a single orchestrator-scoped query
- it should project planned (never-bound) roles and live roles together as plan nodes with their blocking edges
- it should mark a never-bound worker role as planned and a bound (live or ended) role as not planned

**Rendering**

- it should label a node by its short-id parsed from the role-name prefix
- it should fall back to a truncated name label when a role name has no parseable short-id prefix
- it should color a planned node violet with the `○` glyph and a live node by its bound task status (including red `✕` on a failed result)
- it should place nodes in stages by computed longest-path from the blocking edges, not by the short-id number
- it should render the orchestrator's live roles as a flat edgeless stage with a "no plan" hint when no plan is authored

**Parallel groups**

- it should collapse a maximal set of same-stage nodes that share a blocker set and have no internal edges into one range box
- it should render a non-contiguous group as `[first–last +N]`
- it should show aggregate state counts on a collapsed group box

**Partial dependency (option B)**

- it should mark a partially-feeding collapsed group with a `↘` and mark the feeding member with a `↘` on fan-out

**Navigation**

- it should change stage on `↑`/`↓` and collapse any fanned-out group on the way
- it should move within a stage between slots on `←`/`→`
- it should fan out / collapse a group on `Enter`/`Space` when the cursor is on a group
- it should walk members on `←`/`→` inside a fanned-out group and exit+collapse to the adjacent slot when stepping off the edge

**Master-detail header**

- it should show the selected node's name, description, and feeds in the header
- it should show a selected collapsed group's range, members, and downstream in the header

**Sub-coordinator drill-in**

- it should swap the diagram to a sub-coordinator's child orchestrator plan DAG on `Enter` over a sub-coordinator node
- it should pop back to the parent orchestrator's plan DAG on `Esc`
- it should jump to a plain leaf node's agent view on `Enter` (unchanged behavior)

## Open Questions

- **`planview` package location** — sibling `internal/tui/planview` (clean import of `dagview` layout, unit-testable without the rail) vs a type inside `internal/tui/hera`. Leaning sibling package; confirm.
- **Header content for a live node's "description"** — first line of the role's delivery prompt, vs the bound task name, vs nothing. Leaning prompt first line (the substrate persists it); fall back to task name when unbound-but-has-no-prompt is impossible (planned nodes always have a prompt).
- **Drill-in affordance discoverability** — a sub-coordinator node should visually signal it's drillable (e.g. a `▸`/`⊕` glyph) so `Enter`-to-drill isn't a hidden gesture. Confirm the marker.
- **Does the roster stay above the plan graph for a coordinator selection, exactly as it stacks over the tree today?** Leaning yes (roster-over-plan, same geometry as roster-over-tree). Confirm.
