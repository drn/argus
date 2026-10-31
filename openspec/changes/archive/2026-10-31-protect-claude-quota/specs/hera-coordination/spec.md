## ADDED Requirements

### Requirement: Coordinator routing uses Claude-capable account tiers

SpawnHeraCoordinator and MaterializeHeraSubCoordinator SHALL use identical Claude-capable tier filtering and account precedence. If no eligible tier resolves, they SHALL fail open to existing Claude fallback behavior. Explicit backend overrides SHALL remain honored. Builtin Hera skills and MCP help/errors SHALL instruct ordinary workers to omit backend, reserving explicit Claude for workers that need self-promotion, Claude-only connectors/MCP, or in-session Claude review panels.

#### Scenario: Explicit Claude permits personal overflow

- **WHEN** Claude is explicitly requested without an account pin and personal Claude has the best eligible headroom
- **THEN** the backend remains Claude and the task account is personal

#### Scenario: Sub-coordinator inherits an account pin

- **WHEN** materialization supplies an inherited account
- **THEN** it remains above routed tier accounts in precedence

#### Scenario: Ordinary worker guidance

- **WHEN** a coordinator consults shipped spawn guidance for an ordinary worker
- **THEN** the default advice is to omit backend so routing can select available capacity

## MODIFIED Requirements

### Requirement: Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend

coord-hook's context-size stamping, token/cost accrual, and the recycle machinery are Claude-Code-only. The system SHALL therefore guarantee, regardless of `[backend_routing]` tier state or any configured default, that a newly spawned hera coordinator (`SpawnHeraCoordinator`, the rail `n` key and any future caller) or sub-coordinator (`MaterializeHeraSubCoordinator`, a `kind=subcoord` plan node materializing) resolves to a Claude-capable backend (an exact command-basename match against `claude`) whenever a Claude-capable backend is configured.

An explicit caller-supplied backend SHALL still win over this guarantee — a deliberate operator choice, mirroring the pre-existing "explicit backend always wins" precedent — but a non-Claude-capable explicit choice SHALL NOT be accepted silently: the system SHALL log a warning naming what breaks (coord-hook context-size stamping, token/cost accrual, recycle) before honoring it. When no explicit backend is given, the system SHALL first filter routing tiers to Claude-capable backends and use the configured strategy to choose among their backend/account pairs, preserving explicit/project account precedence. If no eligible tier is available, it SHALL use ordinary project/default resolution only if Claude-capable; otherwise it SHALL fail open to the literal `"claude"` backend, also logging a warning, rather than refuse the spawn.

`hera_new_orchestrator` binds an EXISTING task as a coordinator and never resolves or changes that task's backend, since a live session's backend cannot be swapped out from under it. It therefore REJECTS a caller whose backend is not provably Claude-capable (see "hera_new_orchestrator rejects non-Claude callers") instead of letting a non-Claude coordinator come into existence; a caller that already holds a live coordinator binding is exempt so its idempotent re-call keeps the existing "already the coordinator / use hera_join" guidance.

#### Scenario: No explicit backend and ordinary resolution is already Claude-capable

- **WHEN** a coordinator is spawned with no explicit backend and ordinary resolution (project/tier/default) would pick a Claude-capable backend
- **THEN** that backend is used, with no warning logged

#### Scenario: No explicit backend and ordinary resolution would pick a non-Claude-capable backend

- **WHEN** a coordinator is spawned with no explicit backend and a configured `[backend_routing]` tier list would otherwise resolve to a non-Claude-capable backend (e.g. codex, because Claude usage has crossed a configured threshold)
- **THEN** the coordinator's task is stamped with the literal `"claude"` backend instead, and a warning is logged naming what would have broken

#### Scenario: Explicit Claude-capable backend still routes accounts

- **WHEN** a coordinator is spawned with an explicit backend that is Claude-capable (e.g. a custom-named Claude wrapper backend)
- **THEN** that backend is used unchanged, while its eligible tier accounts are selected by the configured strategy unless an account is pinned

#### Scenario: Explicit non-Claude-capable backend is honored, not silently

- **WHEN** a coordinator is spawned with an explicit backend that is NOT Claude-capable
- **THEN** that backend is used (operator override), but a warning is logged naming what breaks

#### Scenario: Sub-coordinator materialization gets the identical guarantee

- **WHEN** the gater materializes a `kind=subcoord` plan node with no explicit backend under the same tier pressure
- **THEN** the materialized sub-coordinator's task is likewise guaranteed Claude-capable, via the same resolution function a root coordinator spawn uses

#### Scenario: hera_new_orchestrator self-promotion is rejected on a non-Claude backend

- **WHEN** a worker role running on a backend that is NOT Claude-capable calls `hera_new_orchestrator` to self-promote to coordinator
- **THEN** the call is rejected with a redirect to `hera_plan_node(kind="subcoord", goal=...)` or, only for a mission requiring worker self-promotion, `hera_spawn_worker(backend="claude")`; the task's backend is never changed and no orchestrator is created (supersedes the earlier "not covered" non-goal; see fix-codex-subcoordinators)
