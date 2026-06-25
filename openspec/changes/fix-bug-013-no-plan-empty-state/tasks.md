## 1. Render the empty-plan state instead of a live-role flat stage

- [x] 1.1 `HeraPage.rebuildPlan` calls `planIsAuthored(nodes, edges)` (≥1 planned
  node OR ≥1 edge) and feeds the embedded widget an EMPTY node set when false, so
  the live worker roles are never drawn as a flat pseudo-DAG stage (BUG-013). The
  projection (`heraPlanNodesWithBridge`) is UNCHANGED — it still surfaces live
  worker nodes (needed when a plan IS authored). Decision lives in the hera layer,
  which owns the "authored plan" semantics; the generic widget stays a node renderer.
- [x] 1.2 `planview.Widget` renders its empty placeholder ("No plan authored." +
  a `hera_plan_node`/`hera_plan` authoring hint via `drawEmptyPlan`) for an empty
  node set (the `len(stages) == 0` path) — improving on the prior
  "No plan — spawn a worker…" text.

## 2. Tests

- [x] 2.1 An orchestrator with live workers but no planned nodes / no edges
  reports `NoPlan() == true` and `Stages() == 0` (the empty-state path), not a
  flat live-role stage.
- [x] 2.2 An orchestrator with ≥1 planned node OR ≥1 edge still reports
  `NoPlan() == false` and projects the full DAG (planned + live nodes + edges).
- [x] 2.3 Render test: the no-plan case draws "No plan authored." and does NOT draw
  the live worker role chips.
- [x] 2.4 Hera page/widget test: a degenerate coordinator (live workers, no plan)
  renders the empty placeholder; a coordinator with an authored plan renders the DAG.

## 3. Docs

- [x] 3.1 Update the `internal/tui/hera/plan.go` doc comment that documented the
  old live-role flat-stage fallback.
- [x] 3.2 Add a gotcha bullet to `context/knowledge/gotchas/dag-rendering.md`
  (no-authored-plan → empty state, not a live-role stage).
