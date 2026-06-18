# Tasks

## 1. Route a worker-bridge sub-coordinator as a coordinator selection

- [x] 1.1 Add `detailsOrch()` to `panes.go`: top-level coordinator → `p.sel.Orch`; worker-bridge sub-coord (`p.sel.BridgeChildOrchID != 0`) → `model.OrchByID(BridgeChildOrchID)`; nil otherwise.
- [x] 1.2 `applySelection`: set `detailsMode = detailsOrch() != nil`; in Details mode feed the HERA pane from `detailsOrch.CoordTaskID()`, unbind the agent pane, `SetOrch(detailsOrch)`, and `rebuildDAG(detailsOrch)`.
- [x] 1.3 Leave `p.sel` (the `SelectionContext` mutation context) pointing at the parent worker role, so Ctrl+D and other mutations are unchanged.
- [x] 1.4 `rebuildDAG` takes the root orchestrator to project instead of reading `p.sel.Orch`.

## 2. Tests

- [x] 2.1 `panes_test.go`: a worker-bridge sub-coord selection yields `detailsMode==true`, unbinds the agent pane, feeds the HERA pane from the sub-coord's own session, and projects the CHILD subtree (root + child worker; not the parent coordinator).
- [x] 2.2 `panes_test.go`: a plain worker still shows the agent terminal (`detailsMode==false`).
- [x] 2.3 `panes_test.go`: a top-level coordinator still shows Details.
- [x] 2.4 `panes_test.go`: the multi-binding bridge row keeps its parent worker mutation context (`SelectionContext().Orch` unchanged) while routing panes to the child.

## 3. Docs + validate

- [x] 3.1 `context/knowledge/gotchas/hera-view.md`: document the bridge-row = coordinator selection pane-routing rule (BUG-004).
- [x] 3.2 `openspec validate hera-subcoord-details --strict` passes.
- [x] 3.3 `make pre-pr` passes clean.
