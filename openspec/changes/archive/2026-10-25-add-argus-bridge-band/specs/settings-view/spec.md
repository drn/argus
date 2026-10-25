## ADDED Requirements

### Requirement: Bridge UI toggles

TUI Settings SHALL expose band and toast toggles in the Bridge section using existing interactions. Both SHALL persist locally and round-trip through the remote store immediately using effective configuration. Web SPA and macOS settings controls SHALL remain the named follow-up bridge-settings-web-macos-parity.

#### Scenario: Remote toggle persists

- **WHEN** a remote TUI toggles bridge.toasts and refreshes
- **THEN** its effective persisted value is shown
