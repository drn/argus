## MODIFIED Requirements

### Requirement: Read-only bridge UI snapshot

GET /api/tasks/{id}/bridge/ui SHALL require existing API authentication and return effective UI settings, bridge liveness, bindings, role status, unread message summaries and context/coordinator budget. Each coordinator binding SHALL carry `workers` with the count of its orchestrator's active worker roles whose Hera status is working and blocked; cancelled planned nodes and roles with no status SHALL NOT count, and other binding kinds SHALL omit the field. Unknown tasks SHALL return 404. Reads SHALL never acknowledge or mark mail read, claim deliveries or mutate roles.

#### Scenario: Coordinator worker roll-up

- **WHEN** a coordinator's orchestrator has one working worker, two blocked workers, a done worker, a worker with no status and a cancelled planned node marked working
- **THEN** the coordinator binding's `workers` is `{"working": 1, "blocked": 2}` and a worker binding on the same task has no `workers` field

#### Scenario: Snapshot preserves mail

- **WHEN** an authenticated client repeatedly reads the snapshot
- **THEN** unread messages and pending deliveries remain unchanged

