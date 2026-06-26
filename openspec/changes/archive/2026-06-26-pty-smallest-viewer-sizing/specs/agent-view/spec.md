## ADDED Requirements

### Requirement: TUI is a focused viewer only while in agent view

The TUI SHALL register itself as an active viewer of a session's PTY, under a
stable per-TUI viewer ID, when it enters the agent view for that task, reporting
the agent pane's current `(cols, rows)`. While in the agent view it SHALL update
the registered size when the pane dimensions change (debounced), rather than
applying an absolute PTY resize. When it leaves the agent view (returns to the task
list, switches to another task, or detaches the session) it SHALL release its
viewer entry so it no longer constrains the session's effective size. The TUI SHALL
NOT unconditionally force a resize on agent-view entry; size reconciliation happens
through the registry minimum.

#### Scenario: Entering agent view registers the pane size
- **WHEN** the user enters the agent view for a task
- **THEN** the TUI registers an active viewer carrying the agent pane's current dimensions

#### Scenario: Leaving agent view releases the claim
- **WHEN** the user returns to the task list or switches away from the task
- **THEN** the TUI removes its viewer entry and the session's effective size is recomputed without it

#### Scenario: Re-entry at the same size does not repaint
- **WHEN** the user re-enters the agent view and the recomputed minimum equals the current PTY size
- **THEN** no resize is issued and the agent is not forced to repaint

#### Scenario: Pane resize updates the registered size
- **WHEN** the agent pane dimensions change while in the agent view
- **THEN** the TUI updates its registered viewer size, and the PTY is resized only if the recomputed minimum changes
