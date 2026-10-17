## MODIFIED Requirements

### Requirement: Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend

coord-hook's context-size stamping, token/cost accrual, and the recycle machinery are Claude-Code-only. The system SHALL therefore guarantee, regardless of `[backend_routing]` tier state or any configured default, that a newly spawned hera coordinator (`SpawnHeraCoordinator`, the rail `n` key and any future caller) or sub-coordinator (`MaterializeHeraSubCoordinator`, a `kind=subcoord` plan node materializing) resolves to a Claude-capable backend (an exact command-basename match against `claude`) whenever a Claude-capable backend is configured.

An explicit caller-supplied backend SHALL still win over this guarantee — a deliberate operator choice, mirroring the pre-existing "explicit backend always wins" precedent — but a non-Claude-capable explicit choice SHALL NOT be accepted silently: the system SHALL log a warning naming what breaks (coord-hook context-size stamping, token/cost accrual, recycle) before honoring it. When no explicit backend is given, the system SHALL run ordinary resolution (project override, then tier list, then default) and use its result only if it is Claude-capable; otherwise it SHALL fail open to the literal `"claude"` backend, also logging a warning, rather than refuse the spawn.

`hera_new_orchestrator` binds an EXISTING task as a coordinator and never resolves or changes that task's backend, since a live session's backend cannot be swapped out from under it. It therefore REJECTS a caller whose backend is not provably Claude-capable (see "hera_new_orchestrator rejects non-Claude callers") instead of letting a non-Claude coordinator come into existence; a caller that already holds a live coordinator binding is exempt so its idempotent re-call keeps the existing "already the coordinator / use hera_join" guidance.

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

#### Scenario: hera_new_orchestrator self-promotion is rejected on a non-Claude backend

- **WHEN** a worker role running on a backend that is NOT Claude-capable calls `hera_new_orchestrator` to self-promote to coordinator
- **THEN** the call is rejected with a redirect to `hera_plan_node(kind="subcoord", goal=...)` or `hera_spawn_worker(backend="claude")`; the task's backend is never changed and no orchestrator is created (supersedes the earlier "not covered" non-goal; see fix-codex-subcoordinators)

## ADDED Requirements

### Requirement: Plan tools advertise sub-coordinator parameters

The system SHALL advertise `kind` (enum `worker`|`subcoord`, default `worker`) and `goal` in the input schema of `hera_plan_node` and of every node object of `hera_plan`, and SHALL NOT mark `prompt` schema-required for those nodes (subcoord nodes supply `goal` instead). Tool descriptions SHALL state that `kind=subcoord` is the way to stand up a sub-coordinator and that it materializes on a Claude-capable backend once its blockers are done, or on the gater's next tick when it has none.

#### Scenario: Schema matches handler

- **WHEN** the advertised schemas for hera_plan_node and hera_plan nodes are compared with the parameters their handlers decode
- **THEN** every decoded parameter is advertised

### Requirement: hera_new_orchestrator rejects non-Claude callers

The system SHALL reject `hera_new_orchestrator` when the calling task's resolved backend is not provably Claude-capable (any resolved command other than an exact `claude` basename, including custom wrappers), with an error telling the caller to ask its coordinator (hera_send) for a `kind=subcoord` plan node or a respawn with `backend="claude"`, or, if unbound, to use the TUI New Coordinator form. The check SHALL be skipped for a caller that already holds a live coordinator binding, and SHALL fail open when the backend cannot be determined (unstamped, unresolvable, or no config). The system SHALL provide no override in the MCP path.

#### Scenario: Codex worker self-promotes

- **WHEN** a task whose backend resolves to a non-Claude command calls hera_new_orchestrator
- **THEN** the call errors with the redirect and no orchestrator is created

#### Scenario: Claude caller

- **WHEN** a task whose backend is Claude calls hera_new_orchestrator
- **THEN** the call succeeds as before
