## ADDED Requirements

### Requirement: Spawned sessions include ~/.local/bin on PATH

The system SHALL ensure `~/.local/bin` is on the PATH of every spawned agent session (new, resumed, recycled), appending it when absent and leaving an inherited PATH that already contains it unchanged.

#### Scenario: Directory missing from inherited PATH
- **WHEN** a command is built while the inherited PATH lacks `~/.local/bin`
- **THEN** the command's PATH ends with `~/.local/bin`

#### Scenario: Directory already present
- **WHEN** the inherited PATH already contains `~/.local/bin`
- **THEN** the PATH is passed through unchanged
