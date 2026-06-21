## MODIFIED Requirements

### Requirement: hera_status updates role status and rolls a finished worker

The system SHALL, on `hera_status`, validate the status as one of idle/working/blocked/done/failed, upsert it on the caller's role, and mirror it to the `task_meta` "hera" namespace best-effort. When a WORKER role reports `done` the system SHALL roll its bound task to in_review and stamp `ready_to_close` via `RollHeraWorkerToReview` — the primary BUG-050 trigger for the idle-but-done case the exit hook misses. When a WORKER role reports `failed` the system SHALL roll its bound task to in_review WITHOUT stamping `ready_to_close` (a failed task is not ready to close — it needs coordinator attention), via the same in_progress-gated, worker-kind-only, idempotent, soft-fail helper family. Both rolls are worker-kind only, no-op unless the task is currently in_progress (so they never clobber a human-set in_review/complete and never auto-complete), leave the live session running, are idempotent, and are soft-fail (a failure never blocks the status update).

Derived from: `internal/mcp/hera.go:643` (`toolHeraStatus`), `internal/mcp/hera.go:691` (BUG-050 worker roll), `internal/tui/hera/ops.go:193` (the same roll mirrored on the rail `s` key).

#### Scenario: Invalid status is rejected

- **WHEN** hera_status is called with a status other than idle/working/blocked/done/failed
- **THEN** the tool errors naming the valid values

#### Scenario: Worker done rolls to in_review

- **WHEN** a worker role reports status=done and its task is in_progress
- **THEN** the task is rolled to in_review and stamped ready_to_close, while the session keeps running

#### Scenario: Worker failed rolls to in_review without ready_to_close

- **WHEN** a worker role reports status=failed and its task is in_progress
- **THEN** the task is rolled to in_review but ready_to_close is NOT stamped, while the session keeps running

#### Scenario: Done roll never clobbers a non-progress task

- **WHEN** a worker reports done but its task is already in_review or complete
- **THEN** RollHeraWorkerToReview no-ops and the status update still succeeds

## ADDED Requirements

### Requirement: failed role-status and explicit failure gating

The system SHALL support `failed` as a fifth hera role-status value alongside idle/working/blocked/done, persisted in the same `hera_role_status` store and accepted everywhere a role-status is accepted (`hera_status`, the `hera_send` status argument, and the rail step keys). The gater SHALL treat a blocker whose role-status is explicitly `failed` as a failed dependency directly — taking precedence over the session-death inference path — so a worker that self-declares defeat holds its dependents and pings the coordinator without waiting for the blocker's session to end.

#### Scenario: failed is an accepted role-status

- **WHEN** a role is set to status=failed via hera_status or the hera_send status argument
- **THEN** the value is persisted and read back as failed

#### Scenario: A failed blocker holds its dependents

- **WHEN** a planned node's blocker has role-status failed
- **THEN** the gater classifies the dependent as held and pings the coordinator, without waiting for the blocker's session to end

### Requirement: Reopen via the required send-status

The system SHALL flip a `done` or `failed` worker role back to `working` when the worker reports `working` again — which the required worker→coordinator send-status makes structural: a re-engaged worker (its session left running after a finish) reports `working` on its next message by enforcement, with no separate auto-reopen mechanism. A role's status is owned by that role; coordinators reopen a worker by sending it work, not by writing the worker's status.

#### Scenario: A re-engaged worker reopens by self-report

- **WHEN** a worker that previously reported done sends a later message with status=working
- **THEN** its role status flips from done back to working

#### Scenario: Planned dependents re-wait when a blocker reopens

- **WHEN** a blocker that was done returns to working before its dependent has materialized
- **THEN** the gater reads the current status and keeps the dependent planned (it does not materialize off the stale done)

### Requirement: Gater held-state re-arm and recovery notice

The system SHALL re-arm the gater's held-ping dedup so a held node is re-reported after its blocker recovers and re-fails, and SHALL announce a recovery. On each tick, for any `(node, blocker)` pair previously pinged as held, the system SHALL clear the dedup entry when the blocker's outcome is no longer failed (it recovered, or the edge/role vanished). When the entry clears specifically because the blocker recovered, the system SHALL emit exactly one "unblocked" notice to the coordinator (sent from the held node's own role so the self-send guard never trips). The system SHALL NOT emit any notice for an already-materialized node whose blocker later reopens (that node cannot be un-spawned).

#### Scenario: Recovery clears the dedup and notifies once

- **WHEN** a node was pinged as held behind a failed blocker and that blocker later recovers
- **THEN** the dedup entry is cleared and exactly one "unblocked" notice is sent to the coordinator

#### Scenario: Re-failure after recovery re-pings

- **WHEN** a blocker recovers (clearing the held dedup) and then fails again
- **THEN** the gater pings the coordinator again for the held node

#### Scenario: No notice for an already-running node's reopened blocker

- **WHEN** a node has already materialized and one of its blockers later reopens
- **THEN** no notice is emitted (the running worker cannot be un-spawned)

### Requirement: Coordinator plan-mutation verbs

The system SHALL provide three coordinator-only MCP verbs to reconcile a plan to reality, each guarded so a non-coordinator caller is rejected:

- `hera_plan_node_update(cwd, name, [prompt], [project], [orchestrator])` — edit a PLANNED node's prompt and/or project. It SHALL be rejected when the named node has already materialized (holds a binding), since the prompt is already delivered.
- `hera_unblock(cwd, blocked, blocker, [orchestrator])` — remove one blocking edge between two roles in the orchestrator. Removing a non-existent edge is an idempotent no-op success. Re-pointing an edge is unblock + block; there is no separate re-point verb.
- `hera_plan_node_cancel(cwd, name, [orchestrator])` — move a PLANNED node to the cancelled terminal state (see the cancelled-node requirement). Cancelling a node that has already materialized SHALL be rejected (a running worker is stopped via the task lifecycle, not the plan).

#### Scenario: Update edits a planned node's prompt

- **WHEN** a coordinator calls hera_plan_node_update with a new prompt for a planned node
- **THEN** the node's stored prompt is updated and will be delivered when it materializes

#### Scenario: Update is rejected for a materialized node

- **WHEN** a coordinator calls hera_plan_node_update for a node that already holds a binding
- **THEN** the call is rejected because the prompt is already delivered

#### Scenario: Unblock drops an edge and is idempotent

- **WHEN** a coordinator calls hera_unblock for an existing edge, then again for the same pair
- **THEN** the first removes the edge and the second succeeds as a no-op

#### Scenario: Mutation verbs reject non-coordinators

- **WHEN** a worker or freelance role calls any of hera_plan_node_update, hera_unblock, or hera_plan_node_cancel
- **THEN** the call is rejected as coordinator-only

### Requirement: Cancelled planned nodes are kept, gated out, and unblock dependents

The system SHALL represent a cancelled planned node with a `cancelled_at` marker on the role rather than deleting it, so the node remains in the plan for visibility. A cancelled node SHALL be excluded from gater materialization (it never spawns), and SHALL no longer gate its dependents: a dependent blocked only by cancelled (and otherwise-satisfied) blockers SHALL be free to materialize. The role and its edges are NOT removed, so the plan view can still render the cancelled node distinctly.

#### Scenario: A cancelled node never materializes

- **WHEN** a planned node is cancelled
- **THEN** the gater excludes it from materialization on every subsequent tick

#### Scenario: Dependents of a cancelled node proceed

- **WHEN** a planned node's only unsatisfied blocker is cancelled
- **THEN** the gater no longer treats it as blocking and the dependent becomes eligible to materialize

#### Scenario: A cancelled node is kept in the plan

- **WHEN** a node is cancelled
- **THEN** its role and edges remain in the store (not deleted) so the plan view can render it

### Requirement: The plan-DAG is the authoritative coordination tracker

The system's coordinator guidance SHALL declare that, while a coordinator holds a live binding, the hera plan-DAG is the single source of truth for its multi-agent work: coordinators author and reconcile worker activity through the `hera_plan*` verbs (including update/unblock/cancel) and SHALL treat the harness task-creation reminder as not applicable to coordinated work. This guidance lives in the in-repo hera skill.

#### Scenario: The skill declares the DAG authoritative

- **WHEN** a coordinator with a live binding consults the hera skill
- **THEN** the skill instructs it to reconcile all worker activity through the plan-DAG verbs rather than the harness task list
