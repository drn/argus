## MODIFIED Requirements

### Requirement: Default configuration

The system SHALL provide a baseline configuration with sensible defaults so that an instance with no stored settings is fully usable.

#### Scenario: Built-in backends present

- **WHEN** a default configuration is produced
- **THEN** it SHALL include backend entries for `claude`, `codex`, `pi`, and `opencode`, each with a command template
- **AND** the `opencode` entry SHALL use the interactive `opencode mini` command with `--prompt` as its prompt flag, submitting the initial prompt without user input; launch-time auto approval is governed by agent execution rather than stored in the template
