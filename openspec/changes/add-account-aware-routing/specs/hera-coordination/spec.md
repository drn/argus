## ADDED Requirements

### Requirement: Coordinator and sub-coordinator spawn select a Claude account from an ordered list

When `[backend_routing].coordinator_accounts` is configured, a newly spawned hera coordinator (`SpawnHeraCoordinator`) or sub-coordinator (`MaterializeHeraSubCoordinator`) with no explicit account SHALL walk that list in order and run on the first account whose cached weekly Claude reading is below `coordinator_threshold_pct`. An unknown or stale reading SHALL count as available. An account that is not configured, cannot run Claude, or whose folder is not signed in SHALL be skipped and logged. Accounts that resolve to the same signed-in identity SHALL be treated as one, with `default` preferred (see `backend-tier-routing`). If every listed account is at or above the limit, the listed usable account below the routing ceiling with the lowest reading SHALL be used; only when every listed account is at or above the routing ceiling does the coordinator leave Claude (see "Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend"). An explicit account SHALL always win. This selection SHALL NOT itself change the coordinator's backend, which is governed by "Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend". When `coordinator_accounts` is unset, coordinator account resolution SHALL be unchanged.

#### Scenario: Work under the coordinator limit is used

- **WHEN** `coordinator_accounts = ["default", "personal"]`, `coordinator_threshold_pct = 95`, and `default` (work) is at 80%
- **THEN** a new coordinator runs on `default`

#### Scenario: Work over the coordinator limit falls back to personal

- **WHEN** `default` (work) is at 97% and `personal` is at 36%, with the same configuration
- **THEN** a new coordinator runs on `personal`

#### Scenario: Workers stop before coordinators do

- **WHEN** `default` is at 80%, its worker tier threshold is 70, and `coordinator_threshold_pct` is 95
- **THEN** routed workers do not select `default`, while a new coordinator still runs on `default`

#### Scenario: All listed accounts over the limit

- **WHEN** every account in `coordinator_accounts` is at or above `coordinator_threshold_pct` but at least one is below the routing ceiling
- **THEN** the coordinator stays on Claude on the listed account with the lowest reading, and the spawn succeeds

#### Scenario: Unknown reading counts as available

- **WHEN** `default` has no usage reading yet
- **THEN** a new coordinator runs on `default`

#### Scenario: Same login under both accounts

- **WHEN** `default` and `personal` are signed into the same login
- **THEN** a new coordinator runs on `default`

#### Scenario: Sub-coordinator materialization uses the same selection

- **WHEN** the gater materializes a `kind=subcoord` plan node with no explicit account while `default` is over the coordinator limit
- **THEN** the sub-coordinator runs on `personal`, via the same selection a root coordinator spawn uses

#### Scenario: Explicit account wins

- **WHEN** a coordinator is spawned with `account: "personal"` while `default` is under the limit
- **THEN** the coordinator runs on `personal`

#### Scenario: Unset list leaves behavior unchanged

- **WHEN** `coordinator_accounts` is not configured
- **THEN** coordinator account resolution is exactly as before this change

## MODIFIED Requirements

### Requirement: Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend

coord-hook's context-size stamping, token/cost accrual, and the recycle machinery are Claude-Code-only. The system SHALL therefore guarantee that a newly spawned hera coordinator (`SpawnHeraCoordinator`, the rail `n` key and any future caller) or sub-coordinator (`MaterializeHeraSubCoordinator`, a `kind=subcoord` plan node materializing) resolves to a Claude-capable backend (an exact command-basename match against `claude`), regardless of `[backend_routing]` tier pressure, threshold crossings, a project backend, or any configured default. Crossing a worker tier threshold or `coordinator_threshold_pct` SHALL NOT move a coordinator off Claude.

The single exception is a Claude budget that is exhausted: when every Claude account the coordinator could run on is at or above the routing ceiling — the usable accounts in `coordinator_accounts` when that list is set, otherwise the account ordinary account resolution selects — the system SHALL fall back to the first configured non-Claude `[[backend_routing.tier]]` whose reading is below the routing ceiling, and SHALL log a warning naming what breaks (coord-hook context-size stamping, token/cost accrual, recycle). The routing ceiling is `[backend_routing].ceiling_pct`, default 98 (see `backend-tier-routing`: "No agent is created on an account at or above the routing ceiling"); a reading counts against it when its cached weekly used percentage is at or above the ceiling, and a held depleted reading always counts. An unknown or stale reading SHALL count as having budget, so the coordinator stays on Claude. If no non-Claude tier is configured, or every one is also at or above the ceiling, the spawn SHALL be refused with an error naming each candidate and its reading; a refused sub-coordinator materialization SHALL leave the plan node planned and count as a materialize failure, so the existing consecutive-failure escalation pings the parent coordinator.

An explicit caller-supplied backend SHALL still win over this guarantee — a deliberate operator choice, mirroring the pre-existing "explicit backend always wins" precedent — but a non-Claude-capable explicit choice SHALL NOT be accepted silently: the system SHALL log a warning naming what breaks before honoring it. When no explicit backend is given and Claude is not exhausted, the system SHALL run ordinary resolution (project override, then tier list, then default) and use its result only if it is Claude-capable; otherwise it SHALL fail open to the literal `"claude"` backend, also logging a warning, rather than refuse the spawn.

This requirement does NOT cover `hera_new_orchestrator`, which binds an EXISTING task as a coordinator and never resolves or changes that task's backend — a worker already running on a non-Claude-capable backend that self-promotes to coordinator via `hera_new_orchestrator` keeps that backend. A live session's backend cannot be swapped out from under it; this is a named, structural gap, not an oversight.

#### Scenario: No explicit backend and ordinary resolution is already Claude-capable

- **WHEN** a coordinator is spawned with no explicit backend and ordinary resolution (project/tier/default) would pick a Claude-capable backend
- **THEN** that backend is used, with no warning logged

#### Scenario: No explicit backend and ordinary resolution would pick a non-Claude-capable backend

- **WHEN** a coordinator is spawned with no explicit backend and a configured `[backend_routing]` tier list would otherwise resolve to a non-Claude-capable backend (e.g. codex, because Claude usage has crossed a configured threshold) while at least one Claude account is below the routing ceiling
- **THEN** the coordinator's task is stamped with the literal `"claude"` backend instead, and a warning is logged naming what would have broken

#### Scenario: Claude over every threshold but not exhausted stays on Claude

- **WHEN** `coordinator_accounts = ["default", "personal"]`, `default` is at 100% and `personal` is at 97% with `coordinator_threshold_pct = 95` and the default ceiling of 98, and a Codex tier is at 20%
- **THEN** a new coordinator runs on Claude on `personal`, not on Codex

#### Scenario: Every Claude account at the ceiling falls back to Codex

- **WHEN** `default` is at 100% and `personal` is at 98% with the default ceiling of 98, and a configured Codex tier is at 20%
- **THEN** a new coordinator runs on the Codex backend, and a warning is logged naming coord-hook context-size stamping, token/cost accrual and recycle

#### Scenario: Sub-coordinator refused at the ceiling stays planned

- **WHEN** the gater materializes a `kind=subcoord` plan node while every Claude account and every non-Claude tier is at or above the routing ceiling
- **THEN** the node stays planned, the attempt counts as a materialize failure, and the parent coordinator is pinged once the existing escalation threshold is reached

#### Scenario: Ceiling is configurable

- **WHEN** `ceiling_pct = 100`, `default` is at 100% and `personal` is at 99%
- **THEN** a new coordinator runs on Claude on `personal`

#### Scenario: Unknown Claude reading is not exhaustion

- **WHEN** `default` is at 100% and `personal` has no usage reading yet
- **THEN** a new coordinator runs on Claude on `personal`

#### Scenario: Everything at the ceiling refuses the spawn

- **WHEN** every Claude account and every configured non-Claude tier is at or above the routing ceiling, or no non-Claude tier is configured
- **THEN** the coordinator spawn is refused with an error naming each candidate and its reading, and no task, worktree or branch is created

#### Scenario: Project backend does not move a coordinator off Claude

- **WHEN** the project's backend is `codex` and Claude is not exhausted
- **THEN** a new coordinator for that project runs on Claude, and a warning is logged

#### Scenario: Explicit Claude-capable backend is honored silently

- **WHEN** a coordinator is spawned with an explicit backend that is Claude-capable (e.g. a custom-named Claude wrapper backend)
- **THEN** that backend is used unchanged, with no warning logged

#### Scenario: Explicit non-Claude-capable backend is honored, not silently

- **WHEN** a coordinator is spawned with an explicit backend that is NOT Claude-capable
- **THEN** that backend is used (operator override), but a warning is logged naming what breaks

#### Scenario: Sub-coordinator materialization gets the identical guarantee

- **WHEN** the gater materializes a `kind=subcoord` plan node with no explicit backend, under the same tier pressure or the same exhaustion
- **THEN** the materialized sub-coordinator resolves exactly as a root coordinator would — Claude unless every Claude account is at or above the routing ceiling — via the same resolution function a root coordinator spawn uses

#### Scenario: hera_new_orchestrator self-promotion is not covered

- **WHEN** a worker role already running on a non-Claude-capable backend calls `hera_new_orchestrator` to self-promote to coordinator
- **THEN** the task's backend is left unchanged — `hera_new_orchestrator` binds the existing task without any backend resolution step, and this is a named non-goal, not a defect in this requirement
