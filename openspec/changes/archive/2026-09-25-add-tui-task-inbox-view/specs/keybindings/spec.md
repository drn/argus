# Keybindings

## ADDED Requirements

### Requirement: Inbox viewer binds to i on the task list and the Hera rail

The keymap SHALL define rebindable actions `tasklist.inbox` and `hera_rail.inbox`, both defaulting to `i`, that open the read-only Inbox modal for the selected task. On the Hera rail the action targets the selected row's bound task and is a no-op when the row has no task. Both actions SHALL appear in the generated help overlay and command palette.

#### Scenario: i opens the inbox from the task list

- **WHEN** a task is selected on the Tasks list and the operator presses `i`
- **THEN** the Inbox modal opens for that task

#### Scenario: i opens the inbox from the Hera rail

- **WHEN** a worker role row with a bound task is selected on the Hera rail and the operator presses `i`
- **THEN** the Inbox modal opens for that role's bound task

#### Scenario: Rebinding is honored

- **WHEN** `[keybindings.tasklist] inbox = "I"` is configured
- **THEN** `I` opens the Inbox from the task list and `i` does not
