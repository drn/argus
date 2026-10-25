## ADDED Requirements

### Requirement: Read-only bridge UI snapshot

GET /api/tasks/{id}/bridge/ui SHALL require existing API authentication and return effective UI settings, bridge liveness, bindings, role status, unread message summaries and context/coordinator budget. Unknown tasks SHALL return 404. Reads SHALL never acknowledge or mark mail read, claim deliveries or mutate roles.

#### Scenario: Snapshot preserves mail

- **WHEN** an authenticated client repeatedly reads the snapshot
- **THEN** unread messages and pending deliveries remain unchanged

### Requirement: Bridge UI settings API

GET and PUT /api/settings SHALL include band and toasts booleans in the bridge section. Partial updates SHALL preserve omitted settings and return effective config.toml-over-DB values. Remote TUI SHALL map both keys through this contract.

#### Scenario: Band-only update

- **WHEN** a client updates bridge.band alone
- **THEN** the API persists it and returns unchanged effective delivery, enabled and toasts values
