## RENAMED Requirements

- FROM: `### Requirement: Redraw-loop liveness is unique per task`
- TO: `### Requirement: PTY sync loop liveness is unique per task`

## MODIFIED Requirements

### Requirement: PTY sync loop liveness is unique per task

While a task's agent view is active, including its visible Tasks rail pane, the system SHALL keep at most one live
PTY sync loop for that task at any time. The loop SHALL keep the session's PTY
size in sync with the pane and SHALL NOT queue repaints; repainting on output
is handled separately (see the terminal-rendering capability). Starting a new
loop for a task SHALL supersede any previous loop still running for that same
task; a superseded loop SHALL stop at its next liveness check regardless of
whether the agent view currently appears to be showing that task again.
Deleting a task SHALL clear its loop liveness marker so it cannot linger in
memory for the remaining life of the process.

#### Scenario: A newer loop supersedes a stale one for the same task

- **WHEN** a new PTY sync loop is started for a task while an older loop for
  that same task is still running (e.g. the view was left and returned to
  faster than the older loop's own check interval)
- **THEN** the older loop's next liveness check reports that it must stop,
  even though the agent view currently shows that task again

#### Scenario: Deleting a task clears its liveness marker

- **WHEN** a task with a tracked loop liveness marker is deleted
- **THEN** the marker is removed so it does not persist for a task that no
  longer exists

#### Scenario: The loop keeps PTY size in sync without repainting

- **WHEN** the pane's size changes while the agent view is active
- **THEN** the loop applies the pending PTY resize off the UI goroutine and does not itself queue a repaint

#### Scenario: The Tasks rail keeps its viewed task's size loop alive

- **WHEN** the Tasks rail is showing a task's terminal while the app remains in task-list mode
- **THEN** the current PTY sync loop remains live for that task, and a hidden rail or a superseding generation stops it
