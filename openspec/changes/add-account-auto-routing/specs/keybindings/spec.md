## ADDED Requirements

### Requirement: Move-to-account binds to A on the task list and the Hera rail

The keymap SHALL define rebindable actions `tasklist.move_account` and `hera_rail.move_account`, both bound to `A` by default, and both SHALL appear in the generated help overlay and the command palette. The action SHALL open a picker listing `auto` and every account other than the selected task's current one that supports its backend, each labelled with its 5-hour and weekly usage percentages ("—" when unknown). Confirming SHALL run the account-move operation and resume the session off the UI thread, reporting the outcome in the status bar. The action SHALL show a status-bar notice instead of the picker when no task is selected, the task's backend is not Claude, no other account applies, or the task's session is live and not idle. In remote mode the move SHALL go through `POST /api/tasks/{id}/account`.

#### Scenario: Move from the task list
- **WHEN** the user presses `A` on an idle task on `default` and confirms `personal`
- **THEN** the task resumes on `personal` and the status bar reports the move

#### Scenario: Busy task
- **WHEN** the user presses `A` on a task whose agent is producing output
- **THEN** a notice is shown and no picker opens

#### Scenario: Rebound
- **WHEN** `[keybindings.tasklist] move_account = "ctrl+a"` is configured
- **THEN** `ctrl+a` opens the picker and `A` does not
