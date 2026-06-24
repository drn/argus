# daemon-focus-tracking Specification

## Purpose
Track which task pane currently holds the user's focus so reliable pane-delivery can gate injection on an unfocused session — providing focus registration/query and an optional focus-transition event, kept correct under concurrent access.
## Requirements
### Requirement: Focus registration and query

The system SHALL maintain a daemon-level `FocusTracker` keyed by task ID that records whether a human is currently focused on a task's pane. `SetFocused(taskID string, focused bool)` registers or clears focus; `IsFocused(taskID string) bool` queries the current state. Both methods SHALL be safe to call concurrently.

#### Scenario: Task starts unfocused

- **WHEN** `IsFocused` is queried for a task that has never had `SetFocused` called
- **THEN** the result is false

#### Scenario: Focus is registered

- **WHEN** `SetFocused(taskID, true)` is called
- **THEN** `IsFocused(taskID)` returns true

#### Scenario: Focus is cleared

- **WHEN** `SetFocused(taskID, false)` is called after a prior `SetFocused(taskID, true)`
- **THEN** `IsFocused(taskID)` returns false

#### Scenario: Concurrent reads and writes are safe

- **WHEN** multiple goroutines call `SetFocused` and `IsFocused` concurrently for the same task ID
- **THEN** no data race occurs

### Requirement: TUI wires focus on agent-view enter and leave

The TUI SHALL call `SetFocused(taskID, true)` when the user enters agent view for a task and `SetFocused(taskID, false)` when the user leaves agent view (including mode change, task switch, or TUI exit). Both the native agent terminal pane and plugin-view input forwarding count as focus, because all human keystrokes flow through the TUI input path.

#### Scenario: Entering agent view registers focus

- **WHEN** the TUI transitions to modeAgent with a non-empty task ID
- **THEN** `SetFocused(taskID, true)` is called

#### Scenario: Leaving agent view clears focus

- **WHEN** the TUI transitions away from modeAgent
- **THEN** `SetFocused(taskID, false)` is called for the task that was in view

#### Scenario: TUI exit clears focus

- **WHEN** the TUI application exits while in agent view
- **THEN** `SetFocused(taskID, false)` is called so no stale focus remains in the tracker

### Requirement: Focus transition emits an event (optional, best-effort)

The system SHOULD emit a `session.focus` event on the events bus when `SetFocused` causes a state change (false→true or true→false). Emission is best-effort: if no sink is installed the emission is a silent no-op. The event payload SHALL carry the task ID and the new focused boolean.

#### Scenario: Focus-gained event fires on enter

- **WHEN** `SetFocused(taskID, true)` is called and the prior state was unfocused
- **THEN** a `session.focus` event with `focused: true` is emitted

#### Scenario: Focus-lost event fires on leave

- **WHEN** `SetFocused(taskID, false)` is called and the prior state was focused
- **THEN** a `session.focus` event with `focused: false` is emitted

#### Scenario: No event on no-op transition

- **WHEN** `SetFocused(taskID, true)` is called and the task is already focused
- **THEN** no event is emitted

