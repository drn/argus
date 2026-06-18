# Hera View

## ADDED Requirements

### Requirement: A worker-bridge sub-coordinator selection shows Details, not the agent terminal (area 6)

The system SHALL route a rail selection that is a worker-bridge sub-coordinator —
a worker ROW that bridges a child orchestrator (its `Selection.BridgeChildOrchID`
is non-zero) — as a COORDINATOR selection: it SHALL enter Details mode for the
bridged CHILD orchestrator (its roster + orchestration tree), unbind the agent
terminal, and feed the HERA (middle) coordinator pane from the child
orchestrator's coordinator task (which is the sub-coordinator's own session). A
top-level coordinator (an orchestrator header, or a coordinator-kind role) SHALL
continue to drive Details mode for its own orchestrator, and a coordinator-spawned
sub-coordinator that already renders as its own orchestrator header SHALL be
unaffected. A plain worker/leaf selection (no bridged child) SHALL continue to
render the agent terminal. Selecting ANY coordinator — top-level or worker-bridge
sub-coordinator — SHALL therefore show its Details pane.

The MUTATION context exposed by `SelectionContext` (the selected role and its
containing orchestrator) SHALL remain the PARENT worker role under its
orchestrator for a worker-bridge sub-coordinator, so mutations (notably the Ctrl+D
cascade) continue to act on the worker role and never the child orchestrator; only
the pane/details/tree ROUTING follows the child.

Derived from: `internal/tui/hera/panes.go` (`applySelection`, `detailsOrch`), `internal/tui/hera/page.go` (`rebuildDAG(root)`), `internal/tui/hera/model.go` (`Selection.BridgeChildOrchID`, `Model.OrchByID`).

#### Scenario: Selecting a worker-bridge sub-coordinator shows the child's Details

- **WHEN** the rail cursor rests on a worker row that bridges a child orchestrator (a sub-coordinator born-bound to both a parent worker role and the child's coordinator role)
- **THEN** Details mode is entered for the child orchestrator (roster + orchestration tree rooted at the child's coordinator), the agent terminal pane is unbound, and the HERA pane feeds from the sub-coordinator's own session

#### Scenario: A plain worker still shows the agent terminal

- **WHEN** the rail cursor rests on a worker that does not bridge any child orchestrator
- **THEN** the agent terminal renders the worker's bound task and Details mode is not entered

#### Scenario: A top-level coordinator still shows Details

- **WHEN** the rail cursor rests on an orchestrator header (the folded coordinator) or a coordinator-kind role
- **THEN** Details mode renders that orchestrator's roster + orchestration tree

#### Scenario: A sub-coordinator selection preserves the parent worker mutation context

- **WHEN** a worker-bridge sub-coordinator row is selected
- **THEN** `SelectionContext` still reports the parent worker role and its orchestrator, so Ctrl+D and other mutations act on the worker role, not the child orchestrator
