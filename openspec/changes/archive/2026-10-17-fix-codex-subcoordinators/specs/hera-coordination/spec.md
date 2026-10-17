## ADDED Requirements

### Requirement: Plan tools advertise sub-coordinator parameters

The system SHALL advertise `kind` (enum `worker`|`subcoord`, default `worker`) and `goal` in the input schema of `hera_plan_node` and of every node object of `hera_plan`, and SHALL NOT mark `prompt` schema-required for those nodes (subcoord nodes supply `goal` instead). Tool descriptions SHALL state that `kind=subcoord` is the way to stand up a sub-coordinator and that it materializes on a Claude-capable backend once its blockers are done, or on the gater's next tick when it has none.

#### Scenario: Schema matches handler

- **WHEN** the advertised schemas for hera_plan_node and hera_plan nodes are compared with the parameters their handlers decode
- **THEN** every decoded parameter is advertised

### Requirement: hera_new_orchestrator rejects non-Claude callers

The system SHALL reject `hera_new_orchestrator` when the calling task's resolved backend is positively identified as not Claude-capable, with an error directing the caller to `hera_plan_node(kind="subcoord", goal=...)` or `hera_spawn_worker(backend="claude")`. The system SHALL fail open when the backend cannot be determined, and SHALL provide no override in the MCP path.

#### Scenario: Codex worker self-promotes

- **WHEN** a task whose backend resolves to a non-Claude command calls hera_new_orchestrator
- **THEN** the call errors with the redirect and no orchestrator is created

#### Scenario: Claude caller

- **WHEN** a task whose backend is Claude calls hera_new_orchestrator
- **THEN** the call succeeds as before
