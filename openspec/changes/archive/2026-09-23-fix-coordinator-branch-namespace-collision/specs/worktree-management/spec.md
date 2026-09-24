## MODIFIED Requirements

### Requirement: Branch namespace for hera-managed worker spawns

Hera worker spawn — both the ad-hoc spawn of a brand-new born-bound worker and the materialization of a pre-planned worker or subcoord node from a plan-DAG — SHALL pass the spawning orchestrator's name as the branch namespace, so the resulting branch is `argus/<orchestrator-name>/<role-name>` instead of `argus/<role-name>`. For a subcoord node (which materializes a new coordinator agent occupying a worker slot in its PARENT orchestrator's plan-DAG), the namespace SHALL be the PARENT orchestrator's name, not the newly minted child orchestrator's name.

A root hera-coordinator spawn (which mints a brand-new top-level orchestrator, rather than occupying a worker slot within an existing one) SHALL ALSO pass its own freshly minted orchestrator's name as the branch namespace, using a branch leaf distinct from the orchestrator name itself (e.g. derived from the coordinator role name), so the resulting branch is `argus/<orchestrator-name>/<distinct-leaf>` and never the bare `argus/<orchestrator-name>` form. This SHALL apply even though the coordinator has no separate pre-existing orchestrator to namespace under — it namespaces under the orchestrator it itself just created, so that orchestrator's name is NEVER used as a leaf ref anywhere, only ever as a directory prefix. This guarantees a worker later spawned under the same orchestrator (which namespaces under that same name per this requirement's first paragraph) can never collide with the coordinator's own branch.

Orchestrator-name resolution for the namespace SHALL fail open: if the orchestrator cannot be resolved, the spawn SHALL proceed with an empty namespace (the flat `argus/<role-name>` form) rather than aborting on that account alone.

Plain, non-hera task creation (`task_create`, the TUI's plain new-task flow) SHALL NOT be namespaced — it has no orchestrator to namespace under, and continues to produce the flat `argus/<task-name>` branch unchanged.

#### Scenario: Ad-hoc worker spawn is namespaced under its orchestrator

- **WHEN** a coordinator in orchestrator "checkout-revamp" spawns a worker with role name "cart-api" via `hera_spawn_worker`
- **THEN** the resulting task's branch is `argus/checkout-revamp/cart-api`

#### Scenario: Plan-DAG materialized worker is namespaced under its orchestrator

- **WHEN** a plan-DAG node named "2b-impl" in orchestrator "my-orch" materializes into a live worker
- **THEN** the resulting task's branch is `argus/my-orch/2b-impl`

#### Scenario: Subcoord node is namespaced under the parent orchestrator, not the child

- **WHEN** a subcoord plan-DAG node named "3a-auth" in parent orchestrator "parent-orch" materializes (minting a new child orchestrator for its own team)
- **THEN** the resulting task's branch is `argus/parent-orch/3a-auth`, not namespaced under the newly minted child orchestrator

#### Scenario: Root coordinator spawn is namespaced under its own new orchestrator with a distinct leaf

- **WHEN** a root hera-coordinator spawn mints a new orchestrator named "ship-feature" with coordinator role "coord"
- **THEN** the resulting task's branch is namespaced under "ship-feature" (e.g. `argus/ship-feature/coord-ship-feature`) and is never the bare `argus/ship-feature` form

#### Scenario: Root coordinator branch never blocks its own first worker

- **WHEN** a root hera-coordinator spawn for orchestrator "ship-feature" is immediately followed by a `hera_spawn_worker` call under that same orchestrator
- **THEN** the worker's branch `argus/ship-feature/<role-name>` is created successfully — the coordinator's own earlier branch never occupies the bare `argus/ship-feature` leaf ref

#### Scenario: Unresolvable orchestrator falls back to a flat branch

- **WHEN** a worker spawn's orchestrator id cannot be resolved to an orchestrator row
- **THEN** the spawn is not aborted solely for that reason, and if it otherwise succeeds the resulting branch uses the flat `argus/<role-name>` form

#### Scenario: Plain task creation is not namespaced

- **WHEN** a plain (non-hera) task is created via `task_create` or the TUI's new-task flow
- **THEN** the resulting branch remains the flat `argus/<task-name>` form, unaffected by this requirement
