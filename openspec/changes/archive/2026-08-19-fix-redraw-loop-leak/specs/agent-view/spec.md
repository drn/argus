# Agent View

## ADDED Requirements

### Requirement: Redraw-loop liveness is unique per task

While a task's agent view is active, the system SHALL keep at most one live
redraw/PTY-sync loop for that task at any time. Starting a new loop for a task
SHALL supersede any previous loop still running for that same task; a
superseded loop SHALL stop at its next liveness check regardless of whether
the agent view currently appears to be showing that task again. Deleting a
task SHALL clear its redraw-loop liveness marker so it cannot linger in
memory for the remaining life of the process.

#### Scenario: A newer loop supersedes a stale one for the same task

- **WHEN** a new redraw loop is started for a task while an older loop for
  that same task is still running (e.g. the view was left and returned to
  faster than the older loop's own check interval)
- **THEN** the older loop's next liveness check reports that it must stop,
  even though the agent view currently shows that task again

#### Scenario: Deleting a task clears its liveness marker

- **WHEN** a task with a tracked redraw-loop liveness marker is deleted
- **THEN** the marker is removed so it does not persist for a task that no
  longer exists
