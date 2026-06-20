## Why

The `add-hera-plan-substrate` change gave hera a durable plan-DAG (planned nodes + `hera_blocks` edges + the gater) but deliberately deferred the rendering, so an authored plan is invisible in the TUI — the Hera Details pane still shows the orchestration *tree* (role hierarchy), not the plan. This change re-points that embedded graph at the plan DAG with the "tight tree" UX locked in the design artifact.

## What Changes

- **Replace the Details-pane orchestration-tree graph with the plan DAG.** Retire `heraTreeNodes`/`tree.go`; the embedded graph renders planned + live roles and their `hera_blocks` edges. (**BREAKING** for the view only — the role-hierarchy graph is gone; the roster still lists every role, and the degenerate no-plan case renders live roles flat.)
- **Short-id labels** parsed from the role-name prefix (`2c-fact-checker` → `2c`); layout stages are computed longest-path from the edges (short-id number is label-only).
- **Auto-collapsing parallel groups** — same-stage nodes that share a blocker set with no internal edges collapse to a `[2a–2c]` range box (`[first–last +N]` when non-contiguous) with aggregate state counts.
- **Partial-dependency option B** — a partially-feeding group carries a `↘`; the feeding member carries a `↘` on fan-out.
- **4-way navigation** — `↑↓` change stage (collapsing any fanned group), `←→` move within a stage, `Enter`/`Space` fan a group out / collapse it; inside a group `←→` walks members and stepping off the edge exits+collapses.
- **Master-detail header** above the diagram — selected node (name / description / feeds) or selected group (range / members / downstream).
- **Sub-coordinator drill-in** — `Enter` on a sub-coordinator node swaps to the child orchestrator's plan DAG via a nav stack; `Esc` pops back. Plain leaf `Enter` still jumps to the task's agent view.
- **Planned-node coloring** — a never-bound role renders violet `○`; a live node colors by its bound task status/result (including failed `✕`).
- **New orchestrator-scoped edge query** `db.ListHeraBlocks(orchID)` surfaced through the `HeraReader` seam into `BuildModel`.
- **Help modal + README keybinding table** updated for the new keys (`Enter`/`Space` fan-out + drill-in, `Esc` drill-out).

## Capabilities

### New Capabilities

_None._ The plan-DAG substrate lives in `task-orchestration` (the substrate change); this view change extends the existing TUI capability instead of adding one.

### Modified Capabilities

- `hera-view`: the "Details region stacks roster over the orchestration tree" and "Orchestration tree projects the role hierarchy in-memory" requirements are replaced by the plan-DAG equivalents; new requirements add short-id labels, parallel-group collapse, partial-dependency option B, 4-way group/member navigation, the master-detail header, sub-coordinator drill-in, planned-node coloring, and the degenerate no-plan rendering.
- `data-persistence`: gains an orchestrator-scoped blocking-edge query (`ListHeraBlocks`).

## Impact

- **Code:** new `internal/tui/planview` widget (reuses `dagview`'s layer math); retire `internal/tui/hera/tree.go` + `tree_test.go`; `internal/tui/hera/page.go` (`rebuildDAG`/`drawDetailsRegion`/`handleDetailsKey`) re-pointed at the plan widget; `internal/tui/hera/model.go` (`RoleView.Planned`, `OrchView.Blocks`) + `reader.go` (`ListHeraBlocks`) + a new `heraPlanNodes` projection; `internal/db/hera_plan.go` (`ListHeraBlocks`); `internal/tui/modal/help.go` + `help_test.go`; README Reference appendix.
- **Specs:** `hera-view`, `data-persistence` deltas.
- **Docs:** `context/knowledge/gotchas/dag-rendering.md` + `gotchas/hera-view.md` updated for the plan-view projection, grouping, and drill-in.
- **No web/API/schema impact:** TUI-only; `hera_blocks` already exists; no SPA counterpart.
- **Depends on** `add-hera-plan-substrate` (same branch).
