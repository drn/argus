## ADDED Requirements

### Requirement: Role-keyed binding aggregates use an index

The store SHALL maintain a non-partial index on `hera_bindings(role_id)` so queries that aggregate or look up bindings by role, including ended bindings (the per-orchestrator cost sums over live and ended bindings), do not scan the whole bindings table. The partial `WHERE ended_at IS NULL` indexes do not satisfy this.

Derived from: `internal/db/schema.go`, `internal/db/hera_cost.go`.

#### Scenario: The per-orchestrator cost sums search by role

- **WHEN** `SumHeraRoleCostAccruedByOrchestrator` or `SumNukedHeraRolesCostByOrchestrator` is planned
- **THEN** the query plan searches `hera_bindings` using an index on `role_id` and does not scan the table

#### Scenario: The index is created on an existing database

- **WHEN** a database created before this change is opened
- **THEN** the index exists afterwards and existing rows are unchanged
