## MODIFIED Requirements

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

The system SHALL, on `hera_spawn_worker`, require the caller to hold a live COORDINATOR binding and create a new argus task (worktree + session) plus, transactionally, a worker role+binding pre-bound to it. The role+binding write is an `AfterPersist` hook inside `agent.CreateAndStart`, joining its LIFO compensating-cleanup stack so any failure unwinds every prior step. The worker's project defaults to the COORDINATOR'S OWN TASK project (authoritative, not `role.ArgusProject`). The role name defaults to a slug of the prompt and is uniquified within the orchestrator. An orientation prefix naming the coordinator + orchestrator is prepended to the delivered prompt; the verbatim prompt is also stored on the role. An optional per-worker `model` is passed through. An optional per-worker `effort` is passed through alongside `model`, subject to the same menu-membership governance as any other spawn path when the resolved archetype is a menu (see diligence-profiles: Menu-based archetype resolution and governance). An optional per-worker `archetype` is passed through to `agent.CreateAndStart` and persisted on the spawned task (and mirrored on the role); when omitted, the worker defaults to the `code_slice` archetype. Required args: `cwd`, `prompt`.

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

#### Scenario: Effort passed through to the worker task

- **WHEN** hera_spawn_worker is called with `effort = "high"`
- **THEN** the spawned worker task carries `high` as its per-spawn effort override

#### Scenario: Off-menu spawn pick substituted

- **WHEN** hera_spawn_worker is called with a `model`/`effort` pair that together do not match any entry
  in the resolved archetype's menu
- **THEN** the spawned worker task's effective model/effort is the menu's first entry instead, and the
  substitution is logged

## ADDED Requirements

### Requirement: hera_retier retiers a live, bound worker

The system SHALL provide a `hera_retier` tool, callable only by a live COORDINATOR binding, that requests a live model/effort change on a bound worker role. The tool SHALL re-resolve the target task's archetype/profile at call time (not from a cached value) and apply the same menu-membership governance as spawn time (see diligence-profiles: Menu-based archetype resolution and governance): a requested pair matching the resolved menu SHALL be honored, and a non-matching pair SHALL be substituted with the menu's first entry, logged. For a Claude-style backend target, the system SHALL deliver the change by writing `/model <model>` and, only if the effort is changing, `/effort <level>`, into the target's PTY through the existing idle-gated single-writer delivery primitive — the same mechanism used for reliable message delivery — never a new write path. For a non-Claude-style backend target, the tool SHALL return an explicit unsupported error rather than silently doing nothing. Required args: `cwd`, `orchestrator`, `role`, `model`, `effort`.

#### Scenario: Non-coordinator caller is rejected

- **WHEN** a worker or freelance role calls hera_retier
- **THEN** the tool errors that only coordinators may retier a worker

#### Scenario: Matching pair delivered to a claude-backend worker

- **WHEN** a coordinator calls hera_retier with a model/effort pair present in the target's resolved menu
- **THEN** the target's PTY receives `/model <model>` and, if the effort differs from its current one,
  `/effort <level>`, delivered through the existing idle-gated delivery primitive

#### Scenario: Off-menu pair substituted and logged

- **WHEN** a coordinator calls hera_retier with a model/effort pair absent from the target's resolved menu
- **THEN** the delivered pair is the menu's first entry instead, and the substitution is logged

#### Scenario: Unsupported backend returns an explicit error

- **WHEN** hera_retier targets a task whose resolved backend is not Claude-style
- **THEN** the tool returns an explicit unsupported-backend error and writes nothing to the target's PTY

#### Scenario: Unchanged effort is not re-sent

- **WHEN** a requested pair's effort matches the target's currently resolved effort
- **THEN** only `/model` is written to the target's PTY, not `/effort`
