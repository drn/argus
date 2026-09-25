## MODIFIED Requirements

### Requirement: Default configuration

The system SHALL provide a baseline configuration with sensible defaults so that an instance with no stored settings is fully usable.

#### Scenario: Built-in backends present

- **WHEN** a default configuration is produced
- **THEN** it SHALL include backend entries for `claude`, `codex`, `pi`, and `opencode`, each with a command template
- **AND** the `opencode` entry SHALL use the full interactive `opencode` command with `--prompt` as its prompt flag, submitting the initial prompt when its model is ready; launch-time auto approval is governed by agent execution rather than stored in the template

#### Scenario: Previous OpenCode default is upgraded

- **WHEN** an existing backend row has the exact previous built-in command `opencode mini` and prompt flag `--prompt`
- **THEN** Argus SHALL update its command to `opencode` on database open
- **AND** a customized command or prompt flag SHALL remain unchanged
