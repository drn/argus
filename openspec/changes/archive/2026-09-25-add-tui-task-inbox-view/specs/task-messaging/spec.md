# Task Messaging

## ADDED Requirements

### Requirement: Read-only TUI inbox viewer

The TUI SHALL provide a read-only Inbox modal for a selected task that lists every message addressed to that task from both message stores, merged in oldest-first order: `task_messages` rows whose recipient is the task (read and unread), and `hera_messages` rows whose recipient role is any hera role the task is or was bound to (read and unread). Each entry SHALL show its timestamp, source store, sender, kind (task messages) or tldr (hera messages), read state, delivery mode and delivered time (hera messages), and full body. Opening, scrolling, or reloading the modal SHALL NOT change any message's read state. Messages SHALL be loaded off the UI thread. In remote TUI mode the modal SHALL show that the viewer is unavailable instead of loading.

#### Scenario: Viewer shows read and unread task messages

- **WHEN** a task has one acknowledged and one unacknowledged `task_messages` row and the operator opens its Inbox
- **THEN** both messages are listed, marked `read` and `unread` respectively

#### Scenario: Viewer includes hera messages for bound roles

- **WHEN** a task is bound to a hera role that has received a hera message, and the operator opens the task's Inbox
- **THEN** the hera message is listed with its sender role name, tldr, body, and delivery state

#### Scenario: Viewing does not mark messages read

- **WHEN** the operator opens, scrolls, and reloads the Inbox of a task with unread messages
- **THEN** those messages remain unread in both stores

#### Scenario: Empty inbox

- **WHEN** the operator opens the Inbox of a task with no messages in either store
- **THEN** the modal shows an empty-inbox placeholder

#### Scenario: Remote mode

- **WHEN** the TUI runs in `--remote` mode and the operator opens a task's Inbox
- **THEN** the modal states the viewer is not available in remote mode and performs no load
