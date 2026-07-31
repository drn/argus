## ADDED Requirements

### Requirement: hera_revive coordinator PULL-revive of a bound role

The system SHALL provide a coordinator-only `hera_revive(cwd, role_name, [orchestrator])` MCP tool that inspects one role the caller coordinates and, based on its live session state, takes exactly one of the following actions, always reporting which one:

- **No live session** (dead, of any role kind including a nested coordinator) → restart it in place, resuming via `--session-id` when the task carries one. Reports `restarted_dead`.
- **Live, role kind is `coordinator`** → left untouched (a live coordinator is presumed operator-interactive). Reports `skipped_coordinator_live`.
- **Live, non-coordinator, no session id to resume** → left untouched. Reports `skipped_no_session_id`.
- **Live, non-coordinator, a kick/restart is already in flight** → left untouched. Reports `skipped_restart_pending`.
- **Live, non-coordinator, not idle** (busy) → left untouched. Reports `skipped_busy`.
- **Live, non-coordinator, idle, but blocked on a user prompt** (selection UI or trailing question) → left untouched, so the pending question is never dismissed. Reports `skipped_blocked_on_prompt`.
- **Live, non-coordinator, idle, not blocked** (genuinely stuck — e.g. SIGTSTP'd by a session-supervisor restart) → kicked (stop + resume in place at its existing PTY size), and, on success, restored from `in_review` back to `in_progress` via the shared `ReviveHeraWorkerToInProgress` helper (best-effort; a restore failure does not fail the tool call). Reports `kicked_stuck`.

This is the SAME safety gate the TUI's `Enter`-key revive path (`heraReattach`/`reviveHeraWorker`) already enforces, reusing the same underlying primitives (`agent.BlockedOnPrompt`, `db.ReviveHeraWorkerToInProgress`, `agent.SessionRunner.KickRerender`/`StartOrReattach`) via a new shared decision function, `internal/hera.ReviveRole` — see `openspec/changes/add-hera-revive/design.md` D3 for why the TUI's own call site is not itself refactored to share this function.

The tool SHALL reject a non-coordinator caller, an unknown `role_name` within the caller's orchestrator, a `role_name` resolving to the caller's own (live, calling) role, and a role with no live binding (planned-but-not-materialized, or ended).

This is strictly pull/on-demand: nothing in the daemon calls `hera_revive` automatically. A coordinator calls it when it notices no progress from a role (e.g. via `hera_status`/`hera_tree_updates`).

Derived from: `internal/hera/revive.go` (`ReviveRole`), `internal/daemon/revive.go` (`HeraReviveRunner`), `internal/mcp/hera.go` (`toolHeraRevive`).

#### Scenario: Dead role is restarted

- **WHEN** a coordinator calls `hera_revive` on a role with no live session
- **THEN** the session restarts in place (resuming via `--session-id` when the task has one) and the tool reports `restarted_dead`

#### Scenario: Stuck worker is kicked and restored to in_progress

- **WHEN** a coordinator calls `hera_revive` on a live worker role that is idle and not blocked on a prompt
- **THEN** the session is kicked (stopped and resumed in place) and, if the worker was parked in in_review awaiting nothing, its task is restored to in_progress; the tool reports `kicked_stuck`

#### Scenario: Live coordinator is never auto-revived

- **WHEN** a coordinator calls `hera_revive` targeting a live nested coordinator role
- **THEN** nothing is restarted and the tool reports `skipped_coordinator_live`

#### Scenario: Busy role is left alone

- **WHEN** a coordinator calls `hera_revive` on a live, non-idle worker/freelance role
- **THEN** nothing is restarted and the tool reports `skipped_busy`

#### Scenario: Role blocked on a prompt is left alone

- **WHEN** a coordinator calls `hera_revive` on a live, idle worker/freelance role that is parked at a user prompt
- **THEN** nothing is restarted (the pending question is preserved) and the tool reports `skipped_blocked_on_prompt`

#### Scenario: A kick already in flight is not duplicated

- **WHEN** a coordinator calls `hera_revive` on a role that already has a pending kick/restart queued
- **THEN** no second kick is queued and the tool reports `skipped_restart_pending`

#### Scenario: Non-coordinator caller is rejected

- **WHEN** a worker or freelance role calls `hera_revive`
- **THEN** the tool errors that only coordinators may revive a role

#### Scenario: Unknown role name is rejected

- **WHEN** `role_name` does not resolve to any role in the caller's orchestrator
- **THEN** the tool errors that the role was not found

#### Scenario: Targeting one's own role is rejected

- **WHEN** `role_name` resolves to the calling coordinator's own role
- **THEN** the tool errors rather than silently reporting `skipped_coordinator_live`

#### Scenario: A planned-but-unmaterialized or ended role is rejected

- **WHEN** `role_name` resolves to a role with no live binding
- **THEN** the tool errors that the role has no live binding

## MODIFIED Requirements

### Requirement: Native hera_* MCP tool surface

The system SHALL register eighteen native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_move`, `hera_rebind`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`, the three plan-mutation verbs `hera_plan_node_update`, `hera_unblock`, and `hera_plan_node_cancel`, and `hera_revive`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. `hera_revive` SHALL likewise be coordinator-only. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

NOTE: this requirement's tool count/list previously read "fourteen" and omitted the three plan-mutation verbs (a pre-existing drift from when `make-hera-plan-living` added them) — corrected here to the accurate eighteen while adding `hera_revive`, since both changes touch this same sentence.

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

### Requirement: Worker revive restores in_progress

The system SHALL restore a worker-bound task from in_review back to in_progress when its session is genuinely revived/resumed and working again, via the single shared helper `ReviveHeraWorkerToInProgress` — the precise inverse of `RollHeraWorkerToReview`. The restore is worker-kind only, no-ops unless the task is currently in_review (so it never clobbers a human-set complete/pending and never disturbs an already-in_progress task), touches the DB status only (never the session), and is idempotent.

The restore SHALL NOT fire when the worker is awaiting coordinator close-out — that is, when its bound task carries `meta:hera.ready_to_close` (the BUG-050 done / clean-exit stamp) OR any of its live worker roles has a terminal role-status (`done` or `failed`). This guard preserves the PR #707 / BUG-050 invariant: a genuinely-finished worker stays in_review even when its idle session is still alive, because a worker never self-completes — the coordinator/human closes it out or decides on a failure.

Three trigger sites share the helper so they cannot drift:

- The daemon's supervisor-mode startup reattach (`reattachSupervised`) calls it for every task the supervisor confirms ALIVE, so a live worker stranded in in_review by a prior roll or reconcile is restored to in_progress on each bounce (the true orphans the supervisor does NOT report alive still flip the other way, to in_review).
- The TUI's live-session revive (`reviveHeraWorker`, the Enter-key in-place `KickRerender` resume) calls it on a successful kick (local store only; `--remote` mode defers to the live local daemon's own reattach restore).
- The `hera_revive` MCP tool's shared `hera.ReviveRole` primitive calls it on a successful kick, identically to the TUI's own call (add-hera-revive).

Derived from: `internal/db/hera.go` (`ReviveHeraWorkerToInProgress`), `internal/daemon/bounce.go` (`reattachSupervised`), `internal/tui/app.go` (`reviveRestoreInProgress`) + `internal/tui/heraactions.go` (`reviveHeraWorker`), `internal/hera/revive.go` (`ReviveRole`).

#### Scenario: Stranded live worker is restored on reattach

- **WHEN** the supervisor reports a worker's session still alive across a daemon bounce and that worker's task is parked in in_review with no close-out marker
- **THEN** the task is restored to in_progress while true orphans still flip to in_review

#### Scenario: Revived suspended worker returns to in_progress

- **WHEN** a live-but-suspended worker in in_review is revived in place via KickRerender and the kick succeeds
- **THEN** its task is restored to in_progress

#### Scenario: A done or clean-exited worker stays in_review

- **WHEN** a worker carries meta:hera.ready_to_close (reported done or cleanly exited) and its idle session is still alive on revive/reattach
- **THEN** the restore no-ops and the task stays in_review for coordinator close-out

#### Scenario: A failed worker stays in_review

- **WHEN** a worker's role status is failed and its session is still alive on revive/reattach
- **THEN** the restore no-ops and the task stays in_review for coordinator attention

#### Scenario: Non-worker and non-review tasks are untouched

- **WHEN** the task is coordinator-bound, holds no live worker binding, or is not currently in_review (in_progress / complete / pending)
- **THEN** the restore no-ops and the status is left unchanged

#### Scenario: hera_revive's kick restores in_progress identically to the TUI

- **WHEN** a coordinator calls `hera_revive` on a stuck worker and the kick succeeds
- **THEN** the same `ReviveHeraWorkerToInProgress` guard applies (restored unless awaiting close-out), exactly as the TUI's Enter-key kick
