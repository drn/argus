## ADDED Requirements

### Requirement: Codex sessions use Argus-only skill discovery

When building a Codex command, the system SHALL prepare the isolated Codex home and set `CODEX_HOME` in the spawned process only. It SHALL keep SQLite state at the user's normal location through `CODEX_SQLITE_HOME` so session capture and resume continue to work. Preparation failure SHALL be logged and SHALL NOT block launch.

#### Scenario: Child-only environment

- **WHEN** isolated-home preparation succeeds for a Codex command
- **THEN** only the child process receives the isolated `CODEX_HOME` value
- **AND** SQLite state stays at the normal Codex location
