## MODIFIED Requirements

### Requirement: Branch namespace for hera-managed worker spawns

Hera worker spawn — both the ad-hoc spawn of a brand-new born-bound worker and the materialization of a pre-planned worker or subcoord node from a plan-DAG — SHALL resolve the spawning orchestrator's name as the branch namespace in the target source repository, so the resulting branch is `argus/<orchestrator-name>/<role-name>` instead of `argus/<role-name>`. For a subcoord node (which materializes a new coordinator agent occupying a worker slot in its PARENT orchestrator's plan-DAG), the namespace SHALL be the PARENT orchestrator's name, not the newly minted child orchestrator's name.

If the sanitized namespace root is already a local branch leaf, resolution SHALL select a deterministic non-colliding fallback based on the orchestrator ID, reserving suffix space before sanitization. It SHALL inspect packed as well as loose local heads, check fallback candidates, and use the same fallback on repeated spawns with the same blocking refs. Removing a blocking ref MAY restore the ordinary namespace on later spawns; existing branches SHALL remain unchanged. Git/config lookup errors SHALL log and fail open to the ordinary namespace.

A root hera-coordinator spawn (which mints a brand-new top-level orchestrator, rather than occupying a worker slot within an existing one) SHALL ALSO pass its own freshly minted orchestrator's name as the branch namespace, using a branch leaf distinct from the orchestrator name itself (e.g. derived from the coordinator role name), so the resulting branch is `argus/<orchestrator-name>/<distinct-leaf>` and never the bare `argus/<orchestrator-name>` form. This SHALL apply even though the coordinator has no separate pre-existing orchestrator to namespace under — it namespaces under the orchestrator it itself just created, so this spawn creates no bare namespace-root leaf. A worker later spawned under the same orchestrator cannot collide with the freshly created coordinator's own branch at the namespace root. Existing tasks that become coordinators SHALL retain their original branches and use collision-aware worker resolution above.

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

#### Scenario: Existing task promotes itself and spawns workers

- **WHEN** a task on `argus/exec-irl-prep` becomes coordinator of `exec-irl-prep` and spawns workers
- **THEN** workers are created under a deterministic alternative namespace and the coordinator's branch is unchanged

#### Scenario: Planned workers and subcoordinators face the same leaf

- **WHEN** a planned worker or subcoordinator materializes under a parent orchestrator whose namespace root is a local leaf
- **THEN** its branch uses the parent's resolved fallback namespace and materialization succeeds

#### Scenario: Promoted worker spawns its own team

- **WHEN** an existing worker becomes coordinator of an orchestrator whose sanitized root is a local leaf in the target repo
- **THEN** its child workers use a non-colliding fallback and its existing parent branch remains unchanged

#### Scenario: Fallback leaf and long sanitized namespace

- **WHEN** the ordinary root and first fallback are leaves, including packed refs or a long name requiring sanitization
- **THEN** resolution selects the next non-colliding length-safe namespace and repeated spawns use that same namespace

#### Scenario: Source repository cannot be inspected

- **WHEN** config resolution or the local-heads probe fails
- **THEN** namespace resolution logs the failure and returns the ordinary namespace rather than aborting the spawn

### Requirement: Ref-namespace collision surfaces as an ordinary creation failure

Because old-style flat hera branches (`argus/<role>`) and new namespaced branches (`argus/<orchestrator>/<role>`) can coexist in the same repository during and after this change, a git ref-namespace collision is possible: a ref cannot simultaneously be a leaf (e.g. `refs/heads/argus/foo`) and a directory prefix (e.g. `refs/heads/argus/foo/bar`). Hera worker spawn and materialization SHALL resolve namespace-root leaves ahead of creation as specified above. Direct low-level worktree creation and other unresolved collisions SHALL retain ordinary Git failure handling. When such a collision occurs, worktree creation SHALL fail with an ordinary error (the same shape as any other git failure surfaced by this capability) and SHALL leave no partially-created worktree, branch, or database row behind.

#### Scenario: Existing flat branch blocks a new namespaced branch

- **WHEN** a branch `argus/foo` already exists and a new worktree is requested with branch namespace "foo" and task name "bar" (which would require creating `argus/foo/bar`)
- **THEN** worktree creation returns an error, and no worktree directory or branch is left behind for the failed attempt
