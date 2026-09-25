## ADDED Requirements

### Requirement: OpenCode auto approval at launch

The system SHALL include OpenCode's `--auto` flag in every newly built command for a backend whose executable is recognized as OpenCode. This SHALL apply to fresh and resumed sessions, regardless of whether the backend command came from the seed configuration or an existing stored or custom backend entry. The system SHALL include the flag only once if the configured command already contains it, and SHALL leave commands for other backends unchanged. OpenCode's explicit `deny` permission rules SHALL continue to apply.

#### Scenario: Fresh OpenCode session

- **WHEN** a fresh session is built from an OpenCode backend command without `--auto`
- **THEN** the command includes `--auto` before its prompt arguments

#### Scenario: Resumed OpenCode session

- **WHEN** a resumed session is built from an OpenCode backend command without `--auto`
- **THEN** the command includes `--auto` along with `--session <id>`

#### Scenario: Existing auto flag

- **WHEN** the OpenCode backend command already contains `--auto`
- **THEN** the built command contains exactly one `--auto` flag

#### Scenario: Other backend

- **WHEN** a command is built for a backend not recognized as OpenCode
- **THEN** no OpenCode `--auto` flag is injected
