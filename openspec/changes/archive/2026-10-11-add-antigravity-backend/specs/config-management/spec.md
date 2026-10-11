## MODIFIED Requirements

### Requirement: Default configuration

The system SHALL provide a baseline configuration with sensible defaults so that an instance with no stored settings is fully usable.

#### Scenario: Default backend selection

- **WHEN** a default configuration is produced
- **THEN** the default backend SHALL be `claude`

#### Scenario: Built-in backends present

- **WHEN** a default configuration is produced
- **THEN** it SHALL include backend entries for `claude`, `codex`, `pi`, `opencode`, and `antigravity`, each with a command template
- **AND** the `opencode` entry SHALL use the full interactive `opencode` command with `--prompt` as its prompt flag, submitting the initial prompt when its model is ready; launch-time auto approval is governed by agent execution rather than stored in the template
- **AND** the `antigravity` entry SHALL use the bare `agy` command with `--prompt-interactive` as its prompt flag; permission flags are governed by agent execution rather than stored in the template

#### Scenario: Existing OpenCode default is upgraded

- **WHEN** an existing backend row has the exact previous built-in command `opencode mini` and prompt flag `--prompt`
- **THEN** Argus SHALL update its command to `opencode` on database open
- **AND** a row with a customized command or prompt flag SHALL remain unchanged

#### Scenario: Projects map initialized

- **WHEN** a default configuration is produced
- **THEN** the projects collection SHALL be non-nil (an empty, ready-to-populate map) rather than nil

#### Scenario: Default UI preferences

- **WHEN** a default configuration is produced
- **THEN** the UI theme SHALL be `default`, elapsed-time display and icons SHALL both be enabled, and the spinner style SHALL be `progress`

#### Scenario: Default service ports

- **WHEN** a default configuration is produced
- **THEN** the knowledge base port SHALL default to `7742` and the REST API port SHALL default to `7743`
