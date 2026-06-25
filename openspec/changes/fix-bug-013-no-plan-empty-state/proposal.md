## Why

**BUG-013 — the Plan pane renders live worker roles as a pseudo-DAG when no plan is authored.**

When a coordinator has authored no plan, the embedded Plan graph
(`internal/tui/planview`) falls back to rendering the orchestrator's live worker
roles as a single flat edgeless stage under a "no plan authored — live roles:"
hint. This depicts ad-hoc managed agents as if they were a plan-DAG, which
contradicts the model the rest of the Hera view is built on:

- the **plan DAG** is the AUTHORED plan (planned nodes + `hera_blocks` edges), and
- the **rail** is the live agent roster.

Showing the live roster a second time, in the DAG, as a fake single-stage graph
is misleading — the operator reads structure that was never authored.

## What Changes

- **When no plan is authored, the Plan pane renders an EMPTY-PLAN placeholder, not
  the live worker roles as a flat stage.** "No plan authored." with a one-line
  authoring hint (`Author one with hera_plan_node / hera_plan.`). The live agents
  remain visible in the rail (untouched) — they are simply not re-drawn as a
  pseudo-DAG.
- **"Authored plan" is defined as: the orchestrator has ≥1 planned node OR ≥1
  blocking edge.** This is the existing `planview` `hasPlan` predicate; the change
  is purely what its negation (`noPlan`) RENDERS — an empty placeholder instead of
  a flat live-role stage.
- **A sub-coordinator bridge with no planned nodes and no block edges still counts
  as "no authored plan"** (it projects as a live, drillable node, not a planned
  node or an edge) — so it renders the empty state, consistent with the model.
- **When a plan IS authored, the full DAG renders exactly as before** — planned +
  live nodes + edges, with the cancelled/failed states, BUG-010 horizontal scroll,
  BUG-011 fanned-group wrap, and BUG-012 status-step re-projection all preserved.

The projection (`heraPlanNodes` / `heraPlanNodesWithBridge`) is UNCHANGED: it still
projects every live worker role as a node (a live worker that is part of an
authored plan must still render with its task-derived colour/icon). Only the
widget's no-authored-plan RENDERING changes.

## Capabilities

### Modified Capabilities

- `hera-view`: when the selected orchestrator has no planned nodes and no blocking
  edges, the Plan graph now renders an empty-plan placeholder ("No plan authored.")
  instead of the orchestrator's live worker roles as a single flat edgeless stage.

## Impact

- **Modified code:**
  - `internal/tui/planview/planview.go` — the `noPlan` render path draws the
    empty-plan placeholder and returns (no flat live-role stage); `computeStages` /
    `buildSlots` produce zero stages for the no-plan case.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only view rendering — no projection-function or edit-callback change.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
