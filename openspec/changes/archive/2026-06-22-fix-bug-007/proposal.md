## Why

In the native Hera view, navigating the embedded plan-DAG graph and pressing Enter on a plain leaf node is meant to JOIN that node's session — select its role in the rail and focus its agent pane, staying on the Hera tab (the existing "Enter on a plain leaf node jumps to its role within the Hera view" requirement).

But the join silently no-ops when the node's coordinator is COLLAPSED/folded in the left rail (BUG-007). The plan graph projects from the full model, so it surfaces a node even when its coordinator is folded; `jumpToLeaf` then calls `Rail.SelectByTaskID`, which scans only the currently-built `rows` slice. A worker buried under a collapsed coordinator was never appended to `rows`, so `SelectByTaskID` returns false, `jumpToLeaf` logs "no rail row for task=…" and returns — Enter does nothing. A folded coordinator should never be able to swallow the join.

## What Changes

- **Before selecting, expand any folded ancestor coordinators.** `jumpToLeaf` resolves the target role's containing orchestrator(s) from the FULL model (fold-independent) and uncollapses each one's ancestor chain to root, then calls `SelectByTaskID` — which now finds the freshly-built row, so the selection + reattach/focus logic runs unchanged.
- The expand walks the SAME canonical parent chain the rail nests by (`canonicalParents`), handles deeply nested sub-coordinators (the whole chain, not just the immediate parent), and is cycle-guarded. When a fold actually flips it rebuilds the rows and persists like a user Space-toggle, so the reveal survives across rebuilds/restarts.
- Coordinator nodes are unaffected — they route through `OnDrillIn`, not `OnEnter`; only worker leaves reach `jumpToLeaf`.
- The "no rail row" log stays as a genuine fallback for a task truly absent from the model.

## Capabilities

### Modified Capabilities

- `hera-view`: The plan-DAG leaf-Enter join now expands the rail (uncollapses the target's ancestor coordinator chain) before selecting, so a folded coordinator no longer swallows the join.

## Impact

- **Modified code:** `internal/tui/hera/page.go` (`jumpToLeaf` expands ancestors before `SelectByTaskID`); `internal/tui/hera/rail.go` (new `EnsureAncestorsExpanded`); `internal/tui/hera/model.go` (new `OrchIDsForTask`).
- **Tests:** `internal/tui/hera/rail_test.go` (multi-level ancestor expand + `OrchIDsForTask`); `internal/tui/hera/dag_test.go` (leaf-Enter expands a collapsed coordinator then joins).
- **Scope:** isolated to `internal/tui/hera`. No `internal/tui/app.go` change. Read-only over the existing model; remote mode (`p.remote`) stays inert.
- **Data / dependencies:** none — pure in-memory rail fold-state mutation, persisted through the existing `RailStateStore` seam.
