## Why

PR #814 added "Detach (make top-level)" to the `J` coordinator picker so an
operator can un-nest a sub-coordinator. But it CANNOT be triggered on the exact
case it was built for: an already-nested sub-coordinator.

A nested sub-coordinator `C` is rendered in its parent `P` as a worker "bridge"
row (the bridging worker role IS `C`'s coordinator — they share the same
multi-binding argus task — and NO separate child orchestrator header is drawn;
see `internal/tui/hera/rail.go` bridge placement). So selecting the nested
sub-coordinator yields a `Selection` whose `Role.Kind == HeraKindWorker`.

`heraCoordReparentTarget` (`internal/tui/heraactions.go`) only returns ok for a
coordinator-kind role row or a coordinator's own orchestrator header — it
returns false for a worker row. So `heraOpenAdopt` falls through to the
statusbar error "J: select a freelancer or a coordinator to adopt". The
asymmetry: you can NEST a top-level coordinator (selected as its orchestrator
header) but cannot UN-NEST it (now a headerless worker bridge row).

The rail already stamps the child orchestrator id on the bridge row's selection
(`Selection.BridgeChildOrchID`, the SAME field `Ctrl+D` uses to cascade-nuke the
nested subtree). The fix is to teach `J` to recognize that field.

## What Changes

- **`heraCoordReparentTarget` recognizes a worker-bridge row as a re-parentable /
  detachable coordinator.** When the selection is a NON-archived `worker`-kind
  role whose `Selection.BridgeChildOrchID != 0`, it resolves the CHILD
  orchestrator id from `BridgeChildOrchID` and the coordinator task hint from the
  bridge role's task (`BridgeTaskID`, falling back to live `TaskID`), and returns
  ok. This routes the existing `heraAdoptCoordinator` path — the detach sentinel
  and `DetachCoordinator(childOrchID)` op — for a nested sub-coordinator.
- A plain (non-bridging) worker — `BridgeChildOrchID == 0` — is NOT treated as a
  coordinator and still surfaces the "select a freelancer or coordinator"
  feedback. Only a bridge-worker row qualifies.
- Re-parent of a nested coordinator works via the same path (symmetry); cycles
  are still rejected authoritatively by `ReparentCoordinator` (it rejects nesting
  under self/descendant).
- Inert in remote mode (`heraAdoptOps == nil`) — preserved.

No new keybinding (reuses `J`), no help-modal / README keybinding churn.

## Capabilities

- `hera-view` — modifies the `J` adopt/re-parent requirement so the COORDINATOR
  selection definition includes a worker-bridge sub-coordinator row, making
  detach/re-parent reachable for an already-nested sub-coordinator.

## Impact

- `internal/tui/heraactions.go` — `heraCoordReparentTarget` gains a
  bridge-worker branch (resolves the child orch id from `BridgeChildOrchID`).
- `internal/tui/heraactions_test.go` — bridge-worker detach/re-parent reachability
  tests; plain-worker non-qualification regression; existing coordinator-header /
  coordinator-role detach + re-parent unchanged.
- `context/knowledge/gotchas/hera-view.md` — the invariant: a nested
  sub-coordinator is a headerless worker-bridge row; `J` must resolve the child
  orch via `Selection.BridgeChildOrchID` (as `Ctrl+D` already does) to detach it.
- No op-layer change (`DetachCoordinator`/`ReparentCoordinator` already resolve
  everything from the child orchestrator id). Help modal / README unchanged.
