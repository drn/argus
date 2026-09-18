## ADDED Requirements

### Requirement: Worker/freelance spawns consult budget-aware backend resolution before falling through to default precedence

Both `hera_spawn_worker`'s MCP handler and the plan-DAG gater's leaf-worker materialization path (`materializeNode`, excluding the `subcoord` branch) SHALL consult the budget-aware backend resolver (see `usage-budget-routing`) before falling through to the existing `ResolveBackend` project/default precedence. This tier SHALL run only when the caller did not supply an explicit `backend`, and SHALL never apply to coordinator creation (`hera_new_orchestrator`) or sub-coordinator materialization.

#### Scenario: hera_spawn_worker with no explicit backend under budget pressure

- **WHEN** hera_spawn_worker is called with no `backend` argument and the budget switch is active
- **THEN** the spawned worker task's backend is the configured fallback backend rather than the project/default backend

#### Scenario: Plan-DAG leaf-worker materialization under budget pressure

- **WHEN** the gater materializes a leaf worker node (not a `subcoord` node) and the budget switch is active
- **THEN** the materialized task's backend is the configured fallback backend rather than the project/default backend

#### Scenario: Sub-coordinator materialization is unaffected

- **WHEN** the gater materializes a `subcoord` node
- **THEN** the budget-aware resolver is not consulted and the sub-coordinator's backend resolves exactly as it did before this change
