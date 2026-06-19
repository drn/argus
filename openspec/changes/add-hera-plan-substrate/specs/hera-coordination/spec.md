## MODIFIED Requirements

### Requirement: Native hera_* MCP tool surface

The system SHALL register twelve native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, and the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

#### Scenario: Tools require task management

- **WHEN** task management is disabled
- **THEN** the `hera_*` tools report "hera not configured" rather than acting

#### Scenario: Native and plugin hera tools are mutually exclusive

- **WHEN** native Hera is enabled
- **THEN** any plugin tool scoped `hera` is suppressed so only the in-tree tools appear

#### Scenario: Plan-authoring tools are coordinator-only

- **WHEN** a worker or freelance role calls `hera_plan_node`, `hera_block`, or `hera_plan`
- **THEN** the tool errors that only coordinators may author the plan

#### Scenario: Whole-graph submission in one call

- **WHEN** a coordinator calls `hera_plan` with a set of nodes and blocking edges
- **THEN** the planned nodes and their cycle-checked edges are created together

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

The system SHALL, on `hera_spawn_worker`, require the caller to hold a live COORDINATOR binding and create a new argus task (worktree + session) plus, transactionally, a worker role+binding pre-bound to it. The role+binding write is an `AfterPersist` hook inside `agent.CreateAndStart`, joining its LIFO compensating-cleanup stack so any failure unwinds every prior step. The worker's project defaults to the COORDINATOR'S OWN TASK project (authoritative, not `role.ArgusProject`). The role name defaults to a slug of the prompt and is uniquified within the orchestrator. An orientation prefix naming the coordinator + orchestrator is prepended to the delivered prompt; the verbatim prompt is also stored on the role. An optional per-worker `model` is passed through. Required args: `cwd`, `prompt`.

The same born-bound transactional spawn SHALL also be reachable as a **materialization** path against a **pre-created planned role**: instead of minting a fresh role, `agent.CreateAndStart` binds and starts the supplied planned role (created earlier via the plan-authoring tools), reusing the identical `AfterPersist` + LIFO-cleanup machinery. Materialization is the only way a planned node acquires a binding, agent, worktree, and inbox; born-bound `hera_spawn_worker` (no pre-created role) remains the immediate "spawn now" path and is unchanged.

#### Scenario: Non-coordinator caller is rejected

- **WHEN** a worker or freelance role calls hera_spawn_worker
- **THEN** the tool errors that only coordinators may spawn workers

#### Scenario: Worker inherits the coordinator's project

- **WHEN** hera_spawn_worker omits `project`
- **THEN** the worker task is created in the coordinator task's own project

#### Scenario: Spawn failure unwinds cleanly

- **WHEN** the role+binding insert or the later session start fails
- **THEN** the LIFO compensating stack unwinds the task, worktree, and any prior steps, leaving no orphan worktree, branch, or ghost row

#### Scenario: Materialization binds a pre-created planned role

- **WHEN** a planned role is materialized
- **THEN** CreateAndStart binds and starts that existing role (rather than creating a new one), reusing the same AfterPersist and LIFO-cleanup machinery
