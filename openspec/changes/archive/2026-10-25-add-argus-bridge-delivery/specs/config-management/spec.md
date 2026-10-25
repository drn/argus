## ADDED Requirements

### Requirement: Bridge delivery setting overlay

The system SHALL provide bridge.delivery default true, with config.toml overriding DB and an absent key retaining the default. The daemon SHALL resolve this setting for each new delivery and budget request without session restart. bridge.delivery SHALL have no effect when bridge.enabled is false. Disabling delivery SHALL restore legacy PTY delivery and the ordinary Stop-hook budget nudge.

#### Scenario: Explicit config overrides DB

- **WHEN** config.toml sets bridge.delivery=false and DB stores true
- **THEN** effective delivery is false
