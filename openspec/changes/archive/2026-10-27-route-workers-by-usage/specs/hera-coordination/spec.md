## ADDED Requirements

### Requirement: Worker spawn reports its resolved backend and source

`hera_spawn_worker` SHALL include the spawned worker's resolved backend in its result, labeled `explicit` when the caller supplied a `backend` argument and `routed` when it did not. The daemon's spawn-success log entry SHALL carry the same backend and source.

#### Scenario: Omitted backend is reported as routed

- **WHEN** `hera_spawn_worker` is called without a `backend` argument
- **THEN** the result names the backend the task resolved to and labels it `routed`

#### Scenario: Explicit backend is reported as explicit

- **WHEN** `hera_spawn_worker` is called with `backend` set
- **THEN** the result names that backend and labels it `explicit`
