## Why

BUG-004: in the native Hera view the selection→pane mapping is inconsistent for
sub-coordinators.

- Selecting a top-level coordinator → shows the coordinator's Details pane
  (roster + orchestration tree).
- Selecting a worker → shows the agent terminal (right region), the coordinator
  in the middle pane.
- Selecting a SUB-coordinator that is a worker-bridge row → it renders in the
  AGENT pane (treated like a plain worker), so the operator can NEVER see the
  Details view for that sub-coordinator.

Root cause: `applySelection` set `detailsMode = sel.IsCoordinator()`, and
`Selection.IsCoordinator()` is true only for a coordinator-kind role or an
orchestrator header. A worker-bridge sub-coordinator nests as a worker ROW
(`Role.Kind==worker`) that bridges a child orchestrator, so `IsCoordinator()` is
false and it routed to the agent terminal. A coordinator-spawned sub-coordinator
that already renders as its OWN header was unaffected (it selects as a header).

## What Changes

- `applySelection` resolves the orchestrator whose Details/tree/HERA-coordinator
  pane the selection drives via a new `detailsOrch()` helper:
  - top-level coordinator (header or coordinator-kind role) → the selected
    orchestrator (unchanged);
  - worker-bridge sub-coordinator (a worker row whose
    `Selection.BridgeChildOrchID != 0`) → the bridged CHILD orchestrator. The
    bridging worker holds the same argus task the child's coordinator role is
    bound to, so it IS the child's coordinator and its Details view must reflect
    the child's roster + subtree.
- `detailsMode` is now `detailsOrch() != nil`, so selecting ANY coordinator —
  top-level or worker-bridge sub-coord — shows Details (roster + tree), never the
  agent terminal. The HERA (middle) pane feeds from the resolved orchestrator's
  coordinator (== the sub-coord's own session for a bridge row).
- The MUTATION context (`SelectionContext`, i.e. `p.sel`) is left pointing at the
  parent worker role under its orchestrator, so Ctrl+D and the other mutations
  still act on the worker, never the child orchestrator — unchanged conservative
  multi-binding safety. Only pane/details/tree ROUTING follows the child.
- `rebuildDAG` takes the root orchestrator to project (the resolved Details orch)
  instead of always reading `p.sel.Orch`.
- Integrates with BUG-013 (#760): the dead-handle re-resolution in `reconcileOne`
  is untouched; the middle pane still reconciles its (now sub-coord) session.

## Capabilities

### Modified Capabilities

- `hera-view`: A worker-bridge sub-coordinator selection is routed as a
  coordinator selection — Details mode for the bridged child orchestrator — not
  the agent terminal.

## Impact

- **Modified code:** `internal/tui/hera/panes.go` (`applySelection`, new
  `detailsOrch`), `internal/tui/hera/page.go` (`rebuildDAG` takes a root orch).
- **Tests:** `internal/tui/hera/panes_test.go` (sub-coord → Details; multi-binding
  bridge row corrected; plain worker still agent terminal; top-level coord still
  Details).
- **Docs:** `context/knowledge/gotchas/hera-view.md` (bridge-row = coordinator
  selection for pane routing).
- **No new keys** (no help-overlay/README change), **no schema change, no daemon
  RPC, no `screen.Sync()`** — this is selection routing, not rendering. Specs stay
  LOCAL DOCS only; the gate stays `make pre-pr`.
