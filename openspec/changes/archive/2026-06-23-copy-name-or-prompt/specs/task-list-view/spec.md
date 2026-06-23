# task-list-view (delta)

## MODIFIED Requirements

### Requirement: Task selection and status actions

Pressing Enter on a non-complete task SHALL fire the select callback; Enter on a complete task SHALL do nothing. Keyboard actions on the selected task SHALL cycle status forward (`s`) and backward (`S`), toggle archive (`a`), toggle pinned (`P`), rename (`r`), and open the copy-choice menu (`c`). Each action SHALL fire its corresponding callback.

#### Scenario: Enter ignores completed tasks

- **WHEN** the cursor is on a task with complete status and the user presses Enter
- **THEN** the select callback does not fire

#### Scenario: Enter selects an in-progress task

- **WHEN** the cursor is on a non-complete task and the user presses Enter
- **THEN** the select callback fires with that task

#### Scenario: Status cycle keys advance and reverse status

- **WHEN** the user presses `s` (or `S`) on the selected task
- **THEN** the task status advances to its next (or previous) value and the status-change callback fires

#### Scenario: Copy key opens the copy-choice menu

- **WHEN** the user presses `c` on the selected task
- **THEN** the copy callback fires with that task (regardless of whether the prompt is empty), so the caller can present a choice of copying the name or the prompt
