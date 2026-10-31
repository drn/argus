## ADDED Requirements

### Requirement: New-task indicator

The task list SHALL render a `✦` glyph on any task whose task_meta `ui.new` is `"true"`, and a `✦` after the project name on the header of any project containing such a task, including when the project is collapsed. The first time the TUI observes a given new task in a process, it SHALL show the status-bar notice "✦ New task: <name> (<project>)".

#### Scenario: Voice task appears

- **WHEN** a voice-captured task with `ui.new="true"` exists
- **THEN** its row and its project header show `✦` and a one-time status-bar notice appears

### Requirement: New-task indicator clears on open

Opening a task (entering its agent view) in the local TUI SHALL delete its `ui.new` meta. Remote, web, and macOS clients SHALL NOT clear it.

#### Scenario: Opening clears the flag

- **WHEN** the user opens a task flagged `ui.new`
- **THEN** the `✦` markers for that task disappear on the next refresh
