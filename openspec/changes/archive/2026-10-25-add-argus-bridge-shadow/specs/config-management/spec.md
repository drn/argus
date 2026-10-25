## ADDED Requirements

### Requirement: Bridge configuration switch
The configuration SHALL provide `[bridge] enabled` (default true; DB key `bridge.enabled`; config.toml wins over the DB) controlling bridge mod injection into Claude launches and the daemon's shadow comparison.

#### Scenario: Default
- **WHEN** no bridge setting exists
- **THEN** the bridge is enabled

#### Scenario: Explicit disable
- **WHEN** config.toml sets `[bridge] enabled = false`
- **THEN** new Claude launches carry no `--plugin-dir` and the comparator records nothing
