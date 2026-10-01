## REMOVED Requirements

### Requirement: Worker/freelance spawns consult budget-aware backend resolution before falling through to default precedence

**Reason**: `usagebudget.ResolveWorkerBackend` (the bespoke hera-only budget resolver this requirement described) is retired (`fix-backend-routing-semantics`). `agent-execution`'s "Backend resolution precedence" requirement now applies uniformly to every task-creation path, hera worker/freelance spawn included — there is no separate hera-specific resolution step left to consult.

**Migration**: See the new "Worker/freelance spawn has no bespoke backend resolution of its own" requirement below, and `agent-execution`'s "Backend resolution precedence".

## ADDED Requirements

### Requirement: Worker/freelance spawn has no bespoke backend resolution of its own

`hera_spawn_worker`'s MCP handler and the plan-DAG gater's leaf-worker materialization path (`materializeNode`, excluding the `subcoord` branch) SHALL pass the caller's explicit `backend` argument (or empty, when omitted) straight through to task creation (`agent.SpawnHeraWorker`/`agent.MaterializeHeraWorker` → `agent.CreateAndStart`) with no intermediate resolution step. An empty backend SHALL resolve exactly as any other task's would, via `agent-execution`'s shared precedence (project override, then the `[backend_routing]` tiered resolver, then the global default) — hera worker/freelance spawn is an ordinary consumer of that precedence, not a special case.

#### Scenario: Explicit backend passes through unchanged

- **WHEN** `hera_spawn_worker` is called with an explicit `backend`
- **THEN** the spawned worker's task uses that backend unchanged, and no budget-aware resolver of any kind is consulted

#### Scenario: Omitted backend resolves via the shared precedence

- **WHEN** `hera_spawn_worker` is called with no `backend` argument and a `[backend_routing]` tier list is configured
- **THEN** the spawned worker's task resolves its backend via that tier list, exactly as a non-hera task created with no explicit backend would

#### Scenario: Plan-DAG leaf-worker materialization is likewise a plain passthrough

- **WHEN** the gater materializes a leaf worker node (not a `subcoord` node)
- **THEN** it passes an empty backend through to `agent.MaterializeHeraWorker`, which resolves it via the same shared precedence as any other task

### Requirement: Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend

coord-hook's context-size stamping, token/cost accrual, and the recycle machinery are Claude-Code-only. The system SHALL therefore guarantee, regardless of `[backend_routing]` tier state or any configured default, that a newly spawned hera coordinator (`SpawnHeraCoordinator`, the rail `n` key and any future caller) or sub-coordinator (`MaterializeHeraSubCoordinator`, a `kind=subcoord` plan node materializing) resolves to a Claude-capable backend (an exact command-basename match against `claude`).

An explicit caller-supplied backend SHALL still win over this guarantee — a deliberate operator choice, mirroring the pre-existing "explicit backend always wins" precedent — but a non-Claude-capable explicit choice SHALL NOT be accepted silently: the system SHALL log a warning naming what breaks (coord-hook context-size stamping, token/cost accrual, recycle) before honoring it. When no explicit backend is given, the system SHALL run ordinary resolution (project override, then tier list, then default) and use its result only if it is Claude-capable; otherwise it SHALL fail open to the literal `"claude"` backend, also logging a warning, rather than refuse the spawn.

This requirement does NOT cover `hera_new_orchestrator`, which binds an EXISTING task as a coordinator and never resolves or changes that task's backend — a worker already running on a non-Claude-capable backend that self-promotes to coordinator via `hera_new_orchestrator` keeps that backend. A live session's backend cannot be swapped out from under it; this is a named, structural gap, not an oversight.

#### Scenario: No explicit backend and ordinary resolution is already Claude-capable

- **WHEN** a coordinator is spawned with no explicit backend and ordinary resolution (project/tier/default) would pick a Claude-capable backend
- **THEN** that backend is used, with no warning logged

#### Scenario: No explicit backend and ordinary resolution would pick a non-Claude-capable backend

- **WHEN** a coordinator is spawned with no explicit backend and a configured `[backend_routing]` tier list would otherwise resolve to a non-Claude-capable backend (e.g. codex, because Claude usage has crossed a configured threshold)
- **THEN** the coordinator's task is stamped with the literal `"claude"` backend instead, and a warning is logged naming what would have broken

#### Scenario: Explicit Claude-capable backend is honored silently

- **WHEN** a coordinator is spawned with an explicit backend that is Claude-capable (e.g. a custom-named Claude wrapper backend)
- **THEN** that backend is used unchanged, with no warning logged

#### Scenario: Explicit non-Claude-capable backend is honored, not silently

- **WHEN** a coordinator is spawned with an explicit backend that is NOT Claude-capable
- **THEN** that backend is used (operator override), but a warning is logged naming what breaks

#### Scenario: Sub-coordinator materialization gets the identical guarantee

- **WHEN** the gater materializes a `kind=subcoord` plan node with no explicit backend under the same tier pressure
- **THEN** the materialized sub-coordinator's task is likewise guaranteed Claude-capable, via the same resolution function a root coordinator spawn uses

#### Scenario: hera_new_orchestrator self-promotion is not covered

- **WHEN** a worker role already running on a non-Claude-capable backend calls `hera_new_orchestrator` to self-promote to coordinator
- **THEN** the task's backend is left unchanged — `hera_new_orchestrator` binds the existing task without any backend resolution step, and this is a named non-goal, not a defect in this requirement
