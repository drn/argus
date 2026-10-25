## ADDED Requirements

### Requirement: Live bridge UI settings

The system SHALL provide bridge.band and bridge.toasts default true. config.toml SHALL override DB values and absent keys SHALL retain defaults. Running mods SHALL refresh effective settings without restart; both UI settings SHALL be inert when bridge.enabled=false.

#### Scenario: File overlay wins

- **WHEN** config.toml sets bridge.band=false and DB stores true
- **THEN** the effective value remains false
