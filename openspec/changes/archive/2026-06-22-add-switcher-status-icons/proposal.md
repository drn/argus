## Why

The task list renders each task's status as a leading glyph (status icon — spinner / moon / clipboard / ✓ / ○ / needs-input) to the left of the name. The task switcher (Ctrl+K, and the flat Hera picker), however, spelled the status out as a word to the right of the name ("In Progress", "In Review", …). The two surfaces showed the same information two different ways, and the right-hand status text crowded the name column. Showing the same leading icon in the switcher makes the two surfaces read consistently at a glance and reclaims horizontal space for the name.

## What Changes

- Render the task list's status glyph to the **left** of each task name in the task switcher modal (both the grouped Ctrl+K mode and the flat picker mode), using the exact same glyph + color the task list shows for that status / in_progress sub-state.
- Drop the spelled-out status name from the switcher row text. Flat-mode rows keep the project name; grouped-mode rows keep the project in the folder header.
- Extract the status-glyph mapping into a single shared helper (`widget.TaskStatusIcon`) consumed by both the task list (`drawTaskRow`) and the switcher, so the two indicator columns can never drift.

## Capabilities

### Modified Capabilities

- `forms-and-modals`: The task switcher modal now conveys status via a leading icon identical to the task list, instead of a trailing status name.

## Impact

- **New code:** `internal/tui/widget/taskstatusicon.go` (`TaskStatusIcon` + `CurrentSpinnerFrame`, the shared classifier).
- **Modified code:** `internal/tui/taskview/tasklist.go` (`drawTaskRow` calls the shared helper), `internal/tui/taskswitcher.go` (entry carries in_progress sub-state flags; rows draw the icon and drop the status name), `internal/tui/app.go` (`openTaskSwitcher` populates the new flags from the same running/idle/idle-unvisited/needs-input sets that feed the task list).
- **Dependencies:** none.
- **Data:** none.
