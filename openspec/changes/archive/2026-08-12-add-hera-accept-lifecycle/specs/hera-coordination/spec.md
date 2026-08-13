## MODIFIED Requirements

### Requirement: Native hera_* MCP tool surface

The system SHALL register nineteen native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_move`, `hera_rebind`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`, the three plan-mutation verbs `hera_plan_node_update`, `hera_unblock`, and `hera_plan_node_cancel`, `hera_revive`, and `hera_accept`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. `hera_revive` and `hera_accept` SHALL likewise be coordinator-only. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

NOTE: this requirement's tool count/list previously read "fourteen" and omitted the three plan-mutation verbs (a pre-existing drift from when `make-hera-plan-living` added them) – corrected to "eighteen" while adding `hera_revive` (`add-hera-revive`), and now to "nineteen" while adding `hera_accept` (`add-hera-accept-lifecycle`), since all three changes touch this same sentence.

#### Scenario: Tools require task management

- **WHEN** task management is disabled
- **THEN** the `hera_*` tools report "hera not configured" rather than acting

#### Scenario: Native and plugin hera tools are mutually exclusive

- **WHEN** native Hera is enabled
- **THEN** any plugin tool scoped `hera` is suppressed so only the in-tree tools appear

#### Scenario: Plan-authoring tools are coordinator-only

- **WHEN** a worker or freelance role calls `hera_plan_node`, `hera_block`, or `hera_plan`
- **THEN** the tool errors that only coordinators may author the plan

#### Scenario: hera_revive is coordinator-only

- **WHEN** a worker or freelance role calls `hera_revive`
- **THEN** the tool errors that only coordinators may revive a role

#### Scenario: hera_accept is coordinator-only

- **WHEN** a worker or freelance role calls `hera_accept`
- **THEN** the tool errors that only coordinators may accept a role's work

### Requirement: Worker revive restores in_progress

The system SHALL restore a worker-bound task from in_review OR complete back to in_progress when its session is genuinely revived/resumed and working again, via the single shared helper `ReviveHeraWorkerToInProgress` – the precise inverse of `RollHeraWorkerToReview` (for the in_review source) and of `hera_accept`'s completion flip (for the complete source, `add-hera-accept-lifecycle`). The restore is worker-kind only, no-ops unless the task is currently in_review or complete (so it never clobbers a human-set pending and never disturbs an already-in_progress task), touches the DB status only (never the session), and is idempotent.

The restore SHALL NOT fire when the worker is awaiting coordinator close-out – that is, when its bound task carries `meta:hera.ready_to_close` (the BUG-050 done / clean-exit stamp) OR any of its live worker roles has a terminal role-status (`done` or `failed`). This guard is re-evaluated identically regardless of whether the source status is in_review or complete – it preserves the PR #707 / BUG-050 invariant (a genuinely-finished worker is never revived out from under a pending coordinator decision) for BOTH source states, rather than being bypassed for the newer complete source.

Four trigger sites share the helper so they cannot drift:

- The daemon's supervisor-mode startup reattach (`reattachSupervised`) calls it for every task the supervisor confirms ALIVE, so a live worker stranded in in_review by a prior roll or reconcile is restored to in_progress on each bounce (the true orphans the supervisor does NOT report alive still flip the other way, to in_review).
- The TUI's live-session revive (`reviveHeraWorker`, the Enter-key in-place `KickRerender` resume) calls it on a successful kick (local store only; `--remote` mode defers to the live local daemon's own reattach restore).
- The `hera_revive` MCP tool's shared `hera.ReviveRole` primitive calls it on a successful kick, identically to the TUI's own call (add-hera-revive).
- Any of the above, applied to a task an operator previously `hera_accept`-ed and whose session was later stopped (e.g. via the Hera rail's `a` HIDE key) – the same revive path restores it from `complete` back to `in_progress`, making completion revocable (`add-hera-accept-lifecycle`).

Derived from: `internal/db/hera.go` (`ReviveHeraWorkerToInProgress`), `internal/daemon/bounce.go` (`reattachSupervised`), `internal/tui/app.go` (`reviveRestoreInProgress`) + `internal/tui/heraactions.go` (`reviveHeraWorker`), `internal/hera/revive.go` (`ReviveRole`), `internal/hera/accept.go` (`AcceptRole`).

#### Scenario: Stranded live worker is restored on reattach

- **WHEN** the supervisor reports a worker's session still alive across a daemon bounce and that worker's task is parked in in_review with no close-out marker
- **THEN** the task is restored to in_progress while true orphans still flip to in_review

#### Scenario: Revived suspended worker returns to in_progress

- **WHEN** a live-but-suspended worker in in_review is revived in place via KickRerender and the kick succeeds
- **THEN** its task is restored to in_progress

#### Scenario: An accepted (complete) worker is restored on revive

- **WHEN** a worker's task is complete (having been accepted via `hera_accept` or the plan-DAG gater's auto-accept) and its live session is later stopped and revived in place, or reattached across a daemon bounce
- **THEN** the task is restored to in_progress, identically to the in_review case, provided it carries no close-out marker

#### Scenario: A done or clean-exited worker stays parked

- **WHEN** a worker carries meta:hera.ready_to_close (reported done or cleanly exited) and its idle session is still alive on revive/reattach, regardless of whether its task is in_review or complete
- **THEN** the restore no-ops and the task stays at its current status for coordinator close-out

#### Scenario: A failed worker stays parked

- **WHEN** a worker's role status is failed and its session is still alive on revive/reattach, regardless of whether its task is in_review or complete
- **THEN** the restore no-ops and the task stays at its current status for coordinator attention

#### Scenario: Non-worker and pending/in_progress tasks are untouched

- **WHEN** the task is coordinator-bound, holds no live worker binding, or is currently pending or in_progress
- **THEN** the restore no-ops and the status is left unchanged

#### Scenario: hera_revive's kick restores in_progress identically to the TUI

- **WHEN** a coordinator calls `hera_revive` on a stuck worker and the kick succeeds
- **THEN** the same `ReviveHeraWorkerToInProgress` guard applies (restored unless awaiting close-out), exactly as the TUI's Enter-key kick

## ADDED Requirements

### Requirement: hera_accept coordinator accept of a bound role's work

The system SHALL provide a coordinator-only `hera_accept(cwd, role_name, [orchestrator], [message])` MCP tool that marks a role's bound task `complete` – the operator/coordinator-facing counterpart to the worker's own `hera_status(done)` self-report. Unlike the worker-done roll (which requires the task be `in_progress`), `hera_accept` acts from ANY non-complete status (`in_progress`, `in_review`, or otherwise) – the coordinator's explicit accept is authoritative regardless of whether the target already self-reported done.

On a genuine flip, the system SHALL send the target role a check-in message (never a forced session stop or restart) whose default body tells it its work has been accepted and marked complete, and explicitly instructs it to reply with exactly one of: confirming it has no other tasks and is winding down, telling the coordinator it still has more work to do, or a question if it isn't sure which applies. That reply is informational only – it SHALL NOT automatically reopen the task; a premature accept is undone only via the explicit revive path (`ReviveHeraWorkerToInProgress`'s `complete` source, see below), never by the reply's content. An optional `message` is appended to that default body. On a target task that is ALREADY `complete`, the tool SHALL return success describing a no-op – no second status write, no second message – rather than erroring or re-notifying.

The underlying status flip and notification SHALL be implemented by a single shared primitive (`internal/hera.AcceptRole`) also called by the plan-DAG gater's auto-accept (see the `task-orchestration` capability's "Gater auto-accepts a materialized node's blockers" requirement), so the two trigger paths can never drift.

`hera_accept` SHALL share `hera_revive`'s exact caller-authorization shape: the caller MUST hold a live coordinator binding in the target's orchestrator, and the target role MUST NOT be the caller's own role.

Derived from: `internal/hera/accept.go` (`AcceptRole`), `internal/mcp/hera.go` (`toolHeraAccept`), `internal/heragater/heragater.go` (`acceptBlockers`, the gater's caller).

#### Scenario: Accept flips an in-progress worker to complete and notifies it

- **WHEN** a coordinator calls `hera_accept` on a worker role whose task is `in_progress`
- **THEN** the task's status flips to `complete` and the worker role receives a message stating its work was accepted and asking it to reply confirming it is winding down, telling the coordinator it has more work, or asking a question

#### Scenario: Accept flips an in-review worker (the ordinary done-report state) to complete

- **WHEN** a coordinator calls `hera_accept` on a worker role whose task is `in_review` (having already self-reported `hera_status(done)`)
- **THEN** the task's status flips to `complete` identically to the in_progress case

#### Scenario: Accepting an already-complete task is a clean no-op

- **WHEN** a coordinator calls `hera_accept` on a role whose task is already `complete`
- **THEN** the tool reports success with a no-op note; no second status write occurs and no second message is sent

#### Scenario: An optional custom message is appended

- **WHEN** a coordinator calls `hera_accept` with a non-empty `message`
- **THEN** the sent notification includes that message alongside the default acceptance body

#### Scenario: The acceptance message is a closed-loop check-in, not a one-way notice

- **WHEN** `hera_accept` sends its default acceptance message to the target role
- **THEN** the message explicitly instructs the recipient to reply with exactly one of confirming it is winding down, telling the coordinator it has more work to do, or asking a question, and states that the reply never automatically reopens the task

#### Scenario: hera_accept is coordinator-only

- **WHEN** a worker or freelance role calls `hera_accept`
- **THEN** the tool errors that only coordinators may accept a role's work

#### Scenario: hera_accept rejects targeting the caller's own role

- **WHEN** a coordinator calls `hera_accept` naming its own role
- **THEN** the tool errors that the target must be a different role the caller coordinates

#### Scenario: hera_accept never stops or restarts the target's session

- **WHEN** `hera_accept` flips a task's status to complete
- **THEN** the target role's live session, if any, is left completely untouched – no stop, no restart, no detach

### Requirement: Enter refuses to restart a dead-session worker awaiting close-out

Pressing `Enter` on a hera rail row with a DEAD session (no live process at all) SHALL start it via the ordinary dead-session restart path (`startSession`) for a coordinator role, UNCHANGED. For a worker or freelance role, the system SHALL first check the SAME `HeraWorkerAwaitingCloseout` predicate the `Worker revive restores in_progress` requirement's guard uses (`meta:hera.ready_to_close`, or a terminal `done`/`failed` role-status) and, if the task is awaiting close-out, SHALL refuse to restart the session: the task's status is left completely unchanged and a clear status-bar message is surfaced instead (e.g. "Task is closed out – use hera_revive to reopen").

This closes a gap discovered by live-testing `hera_accept`: the dead-session branch previously called `startSession` unconditionally for every role kind, which unconditionally flips the task to in_progress with zero Hera awareness. Because the underlying session had nothing left to resume, it exited almost immediately, and the ordinary post-exit rule then rolled the task to in_review – silently undoing an explicit `hera_accept` (or a self-reported-done worker's `ready_to_close` stamp) even though `Enter` is not itself an explicit revive. The refusal makes the "a premature accept can only be undone via an explicit revive" guarantee (`hera_accept`'s own tool description) hold for every UI trigger, not only the two that happened to call `ReviveHeraWorkerToInProgress` already (the live-session kick and `hera_revive`).

Derived from: `internal/tui/heraactions.go` (`heraReattach`, `heraTaskClosedOut`), `internal/db/hera.go` (`HeraWorkerAwaitingCloseout`).

#### Scenario: Enter on a dead-session accepted (complete) worker is refused

- **WHEN** a worker's task is `complete` (accepted via `hera_accept`) and has no live session, and the operator presses `Enter` on its rail row
- **THEN** no session is started, the task's status stays `complete`, and the status bar shows a closed-out message

#### Scenario: Enter on a dead-session self-reported-done worker is refused

- **WHEN** a worker's task carries `meta:hera.ready_to_close` (or a terminal `done`/`failed` role-status) and has no live session, and the operator presses `Enter` on its rail row
- **THEN** no session is started and the task's status is left unchanged

#### Scenario: Enter still restarts a dead session with no close-out marker

- **WHEN** a worker or freelance task has no live session and carries no close-out marker
- **THEN** `Enter` restarts it exactly as before this change

#### Scenario: Coordinators are unaffected

- **WHEN** a coordinator role's dead session is reattached via `Enter`
- **THEN** it is restarted unconditionally, exactly as before this change – coordinators have no close-out concept

### Requirement: The size-drift kick refuses to auto-restart a closed-out worker

The TUI's size-drift kill+resume "kick" (`handleSessionExitUI`'s `pendingRerenderRestart` branch, which auto-restarts a task's session in place when the operator is still viewing it after a stale-PTY-width repair stop) SHALL, for a worker or freelance task, check the SAME `HeraWorkerAwaitingCloseout` predicate the `Enter refuses to restart a dead-session worker awaiting close-out` requirement's guard uses, evaluated from the task's state as it stood BEFORE this exit's own status-transition logic ran. If the task is awaiting close-out, the kick SHALL skip the restart silently: no status write, no session start, the task left exactly as close-out left it. A coordinator task is unaffected — the kick always proceeds for it, exactly as before this change.

This is a SIBLING gap to the Enter-key requirement above, not a duplicate of it: the size-drift kick is a keypress-less entry point (an operator merely navigating onto a closed-out worker's row at a different terminal width can trigger it) into the exact same unconditional-restart hole `heraReattach`'s Enter-path guard closed — confirmed live via daemon-log correlation (`StopSession` → `StartSession(resume=true)` within milliseconds of the operator viewing the row, no `Enter` press at all). Because this call site has no `hera.Selection` to call `IsWorkerOrFreelance()` on (only a bare task ID), the worker/freelance scoping is resolved via a new predicate, `TaskHoldsLiveHeraWorkerOrFreelanceBinding`, before delegating to the shared close-out check. The pre-transition-snapshot requirement matters because this same exit handler's `StatusInProgress` branch can itself stamp `ready_to_close=true` on a healthy, merely-idle in-progress worker as a side effect of the very same exit event (the BUG-050 roll) — checking the guard afterward would misread that fresh stamp as a pre-existing close-out and wrongly refuse to restart an actively-in-flight worker.

Derived from: `internal/tui/app.go` (`handleSessionExitUI`), `internal/tui/heraactions.go` (`heraKickRestartClosedOut`), `internal/db/hera.go` (`HeraWorkerAwaitingCloseout`, `TaskHoldsLiveHeraWorkerOrFreelanceBinding`).

#### Scenario: The kick refuses to restart an accepted (complete) worker

- **WHEN** a worker's task is `complete` (accepted via `hera_accept`) and the size-drift kick stops its session while the operator is still viewing the row
- **THEN** no session is restarted, the task's status stays `complete`

#### Scenario: The kick refuses to restart a self-reported-done worker

- **WHEN** a worker's task carries `meta:hera.ready_to_close` (or a terminal `done`/`failed` role-status) and the size-drift kick stops its session while the operator is still viewing the row
- **THEN** no session is restarted and the task's status is left unchanged

#### Scenario: The kick still restarts a healthy in-progress worker

- **WHEN** a worker's task is genuinely in_progress (not awaiting close-out) and the size-drift kick stops its session while the operator is still viewing the row
- **THEN** the kick restarts the session in place exactly as before this change, even though the exit handler's own BUG-050 roll may have momentarily stamped `ready_to_close` as a side effect of this same exit

#### Scenario: Coordinators are unaffected

- **WHEN** a coordinator task's session is stopped by the size-drift kick while the operator is still viewing it
- **THEN** the kick restarts it unconditionally, exactly as before this change – coordinators have no close-out concept
