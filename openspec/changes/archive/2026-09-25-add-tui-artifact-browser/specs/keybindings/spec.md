## ADDED Requirements

### Requirement: Artifact browser actions in TUI contexts

The Tasks list SHALL expose a rebindable `tasklist.artifacts` action defaulting to `v`. The agent view SHALL expose a rebindable `agent.artifacts` action defaulting to `ctrl+t`, intercepted before PTY input. Both actions SHALL open the artifact browser for the task active in that context, appear in generated help and the command palette, and restore the prior focus when dismissed.

#### Scenario: Open from Tasks list

- **WHEN** a task is selected and the user invokes `tasklist.artifacts`
- **THEN** the browser opens for that selected task

#### Scenario: Open from agent view

- **WHEN** the user invokes `agent.artifacts` while viewing a task session
- **THEN** the browser opens for that task and the key chord is not sent to the agent PTY

#### Scenario: Dismiss browser

- **WHEN** the user presses Esc or Ctrl+Q in the artifact browser
- **THEN** the browser closes and keyboard focus returns to the view that opened it
