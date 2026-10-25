## ADDED Requirements

### Requirement: Bridge settings section

TUI Settings SHALL provide an extensible Bridge section exposing bridge.enabled and bridge.delivery through existing toggle interactions. Changes SHALL persist and round-trip through the configuration overlay. No new key bindings SHALL be introduced. Web SPA and macOS Bridge settings parity SHALL remain a named follow-up.

#### Scenario: Toggle persists through refresh

- **WHEN** the user toggles Bridge delivery and Settings refreshes
- **THEN** the persisted effective value is displayed

#### Scenario: Remote TUI toggle round-trips immediately

- **WHEN** the remote TUI toggles a Bridge setting
- **THEN** the existing settings API persists its partial update and the effective response refreshes the remote config cache immediately
