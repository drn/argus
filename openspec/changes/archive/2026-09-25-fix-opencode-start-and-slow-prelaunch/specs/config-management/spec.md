## MODIFIED Requirements

### Requirement: Built-in backend defaults

The system SHALL provide built-in Claude, Codex, Pi, and OpenCode backends. The built-in OpenCode backend SHALL use the interactive `opencode mini` command with `--prompt` as its prompt flag so a new task submits its prompt without user input. It SHALL leave OpenCode permissions to the user's own configuration. An explicitly configured backend command SHALL override the built-in default.

#### Scenario: New task uses the built-in OpenCode backend

- **WHEN** a user creates an OpenCode task without overriding the backend command
- **THEN** Argus SHALL launch `opencode mini --prompt <task prompt>`
- **AND** the initial prompt SHALL be submitted by OpenCode at startup

#### Scenario: User supplies a custom OpenCode command

- **WHEN** a user configures a different command for the OpenCode backend
- **THEN** Argus SHALL use that command instead of `opencode mini`

#### Scenario: Existing database has the old built-in OpenCode command

- **WHEN** an existing backend row has the exact previous built-in command `opencode` and prompt flag `--prompt`
- **THEN** Argus SHALL update its command to `opencode mini` on database open
- **AND** a row with a customized command or prompt flag SHALL remain unchanged
