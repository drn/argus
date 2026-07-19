# Hera Coordination

## MODIFIED Requirements

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

The system SHALL, on `hera_spawn_worker`, require the caller to hold a live COORDINATOR binding and create a new argus task (worktree + session) plus, transactionally, a worker role+binding pre-bound to it. The role+binding write is an `AfterPersist` hook inside `agent.CreateAndStart`, joining its LIFO compensating-cleanup stack so any failure unwinds every prior step. The worker's project defaults to the COORDINATOR'S OWN TASK project (authoritative, not `role.ArgusProject`). The role name defaults to a slug of the prompt and is uniquified within the orchestrator. An orientation prefix naming the coordinator + orchestrator is prepended to the delivered prompt; the verbatim prompt is also stored on the role. An optional per-worker `model` is passed through. An optional per-worker `archetype` is passed through to `agent.CreateAndStart` and persisted on the spawned task (and mirrored on the role); when omitted, the worker defaults to the `code_slice` archetype. Required args: `cwd`, `prompt`.

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

#### Scenario: Archetype passed through to the worker task

- **WHEN** hera_spawn_worker is called with `archetype = "ci_loop"`
- **THEN** the spawned worker task carries `ci_loop` as its archetype

#### Scenario: Worker archetype defaults when omitted

- **WHEN** hera_spawn_worker omits `archetype`
- **THEN** the spawned worker task defaults to the `code_slice` archetype
