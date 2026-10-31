## ADDED Requirements

### Requirement: Move-to-account binds to > on the task list and the Hera rail

The keymap SHALL define rebindable actions `tasklist.move_account` and `hera_rail.move_account`, both bound to `>` by default, and both SHALL appear in the generated help overlay and the command palette. The action SHALL open a picker listing `auto` and every account other than the selected task's current one that supports its backend, each labelled with its 5-hour and weekly usage percentages ("—" when unknown). Confirming SHALL run the account-move operation and resume the session off the UI thread, reporting the outcome in the status bar. The action SHALL show a status-bar notice instead of the picker when no task is selected, the task's backend is not Claude, or no other account applies. When the task's session is live and not idle, the picker SHALL still open, its confirmation SHALL state that the running agent will be interrupted, and confirming SHALL perform the move with force. The whole flow SHALL be driven by the TUI and daemon alone and SHALL work when the task's current account has no usage left. In remote mode the move SHALL go through `POST /api/tasks/{id}/account`.

#### Scenario: Move from the task list
- **WHEN** the user presses `>` on an idle task on `default` and confirms `personal`
- **THEN** the task resumes on `personal` and the status bar reports the move

#### Scenario: Works on an exhausted account
- **WHEN** the selected task's account is at 100% of its 5-hour window and the user presses `>` and confirms `personal`
- **THEN** the move completes without any turn on the exhausted account

#### Scenario: Busy task
- **WHEN** the user presses `>` on a task whose agent is producing output
- **THEN** the picker opens with an interrupt warning, and confirming moves the task

#### Scenario: Rebound
- **WHEN** `[keybindings.tasklist] move_account = "ctrl+a"` is configured
- **THEN** `ctrl+a` opens the picker and `>` does not
