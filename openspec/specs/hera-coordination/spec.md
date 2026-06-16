# Hera Coordination

## Purpose

Hera Coordination is the native substrate beneath the Hera View: the role/orchestrator/binding storage model, the nine `hera_*` MCP tools an agent uses to bootstrap and participate in a coordination session, the born-bound worker spawn primitive, the subtree TLDR roll-up, and the daemon-startup binding reconciliation. It runs in-process in the daemon and reuses primitives Argus already owns (the runner, the worktree engine, the SQLite store).

This is a faithful capture of current native behavior, mirroring the plugin's `hera-coordination` capability split. Each requirement cites the `file:line` it was derived from. Where native is materially different from the plugin, a `NOTE:` flags it.

## Requirements

### Requirement: Orchestrator, role, and binding storage model

The system SHALL store orchestrators (`hera_orchestrators`), roles of kind `coordinator`/`worker`/`freelance` (`hera_roles`), and role↔task bindings (`hera_bindings`). It SHALL enforce, via partial unique indexes over `WHERE ended_at IS NULL`: at most one live binding per role, one live binding per (argus task, orchestrator), and one live binding per (worktree path, orchestrator). A single argus task MAY therefore hold live bindings under several distinct orchestrators at once, but never two under the same one.

Derived from: `internal/db/schema.go:447` (live-role unique index), `internal/db/schema.go:448` (live task+orchestrator unique index), `internal/db/schema.go:449` (live worktree+orchestrator unique index), `internal/db/hera.go:71` (role-kind constants).

#### Scenario: Second live binding under the same orchestrator is rejected

- **WHEN** a task already holds a live binding under an orchestrator and a second live binding under the same orchestrator is attempted
- **THEN** the unique index rejects it

#### Scenario: Task may bind under multiple orchestrators

- **WHEN** a task holds a live binding under orchestrator A
- **THEN** it may also hold a live binding under orchestrator B (the constraint is per-orchestrator, not global)

### Requirement: Native hera_* MCP tool surface

The system SHALL register exactly nine native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, and `hera_get_messages`. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

Derived from: `internal/mcp/hera.go:54` (`heraToolDefs`, the nine tools), `internal/mcp/hera.go:202` (`SetHeraService`), `internal/mcp/hera.go:210` (`heraEnabled`).

#### Scenario: Tools require task management

- **WHEN** task management is disabled
- **THEN** the `hera_*` tools report "hera not configured" rather than acting

#### Scenario: Native and plugin hera tools are mutually exclusive

- **WHEN** native Hera is enabled
- **THEN** any plugin tool scoped `hera` is suppressed so only the in-tree tools appear

### Requirement: Caller role resolution from cwd with orchestrator disambiguation

The system SHALL resolve the calling agent's hera role by mapping `cwd` to an argus task, then to that task's live binding+role. When the task holds exactly one live binding the `orchestrator` argument is optional; when it holds two or more, the tool SHALL return a disambiguation error listing the caller's orchestrator names, and the caller MUST re-call with `orchestrator=<name>`. An unbound task SHALL get an error instructing it to call `hera_join` or `hera_new_orchestrator`.

Derived from: `internal/mcp/hera.go:218` (`resolveCallerRole`), `internal/mcp/hera.go:270` (`buildHeraAmbiguousError`).

#### Scenario: Single binding resolves without orchestrator

- **WHEN** the caller's task holds exactly one live binding and no `orchestrator` is passed
- **THEN** the role resolves successfully

#### Scenario: Ambiguous binding lists options

- **WHEN** the caller's task holds two or more live bindings and no `orchestrator` is passed
- **THEN** the tool errors with the list of bound orchestrator names to disambiguate

#### Scenario: Unbound task is guided

- **WHEN** the caller's task holds no live binding
- **THEN** the tool errors instructing it to call hera_join or hera_new_orchestrator

### Requirement: hera_new_orchestrator bootstraps and claims the coordinator role

The system SHALL, on `hera_new_orchestrator`, create (or idempotently fetch) the named orchestrator, then transactionally create a coordinator role bound to the calling task's worktree, persisting the caller's argus project on the role. It SHALL reject the call when the calling task already holds a live binding under that orchestrator (directing the caller to `hera_join`). It SHALL mirror the role to the `task_meta` "hera" namespace best-effort (a mirror failure never undoes local state). Required args: `cwd`, `name`, `coordinator_role_name`.

Derived from: `internal/mcp/hera.go:287` (`toolHeraNewOrchestrator`), `internal/mcp/hera.go:328` (`CreateHeraRoleWithBinding`), `internal/mcp/hera.go:343` (meta mirror).

#### Scenario: First call creates orchestrator + coordinator binding

- **WHEN** a task with no binding under orchestrator X calls hera_new_orchestrator(name=X)
- **THEN** orchestrator X exists, a coordinator role bound to the caller is created, and the binding id is returned

#### Scenario: Re-bootstrap under an already-bound orchestrator is rejected

- **WHEN** the caller already holds a live binding under the target orchestrator
- **THEN** the tool errors and directs the caller to hera_join

### Requirement: hera_join claims an existing role or attaches a new one

The system SHALL support two `hera_join` modes. With `role_name` omitted (claim mode) it SHALL return the caller's existing binding for the resolved orchestrator plus its unread message count, without cancelling any pending doorbell deliveries. With `role_name` + `kind` supplied (attach mode) it SHALL create a new role+binding of kind `worker` or `freelance` (rejecting `coordinator` — that path is `hera_new_orchestrator`), persisting the attaching task's project, optionally setting an initial status, and rejecting a second binding under an orchestrator the task is already bound to. Attach mode requires `orchestrator`.

Derived from: `internal/mcp/hera.go:358` (`toolHeraJoin`), `internal/mcp/hera.go:376` (claim mode), `internal/mcp/hera.go:397` (attach mode + coordinator rejection), `internal/mcp/hera.go:421` (already-bound rejection).

#### Scenario: Claim mode returns the existing binding

- **WHEN** a bound task calls hera_join with no role_name
- **THEN** the response reports its orchestrator, role name, kind, binding id, and unread count, and does NOT cancel pending deliveries

#### Scenario: Attach mode rejects coordinator kind

- **WHEN** hera_join is called in attach mode with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator

#### Scenario: Attach mode creates a worker/freelance role

- **WHEN** an unbound task calls hera_join with orchestrator, role_name, and kind=worker
- **THEN** a new worker role+binding is created and the binding id is returned

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

The system SHALL, on `hera_spawn_worker`, require the caller to hold a live COORDINATOR binding and create a new argus task (worktree + session) plus, transactionally, a worker role+binding pre-bound to it. The role+binding write is an `AfterPersist` hook inside `agent.CreateAndStart`, joining its LIFO compensating-cleanup stack so any failure unwinds every prior step. The worker's project defaults to the COORDINATOR'S OWN TASK project (authoritative, not `role.ArgusProject`). The role name defaults to a slug of the prompt and is uniquified within the orchestrator. An orientation prefix naming the coordinator + orchestrator is prepended to the delivered prompt; the verbatim prompt is also stored on the role. An optional per-worker `model` is passed through. Required args: `cwd`, `prompt`.

Derived from: `internal/mcp/hera.go:708` (`toolHeraSpawnWorker`), `internal/mcp/hera.go:739` (coordinator-only guard), `internal/mcp/hera.go:746` (project resolution), `internal/mcp/hera.go:767` (orientation prefix), `context/knowledge/gotchas/hera-view.md` (shared `agent.SpawnHeraWorker` primitive).

#### Scenario: Non-coordinator caller is rejected

- **WHEN** a worker or freelance role calls hera_spawn_worker
- **THEN** the tool errors that only coordinators may spawn workers

#### Scenario: Worker inherits the coordinator's project

- **WHEN** hera_spawn_worker omits `project`
- **THEN** the worker task is created in the coordinator task's own project

#### Scenario: Spawn failure unwinds cleanly

- **WHEN** the role+binding insert or the later session start fails
- **THEN** the LIFO compensating stack unwinds the task, worktree, and any prior steps, leaving no orphan worktree, branch, or ghost row

### Requirement: hera_status updates role status and rolls a finished worker

The system SHALL, on `hera_status`, validate the status as one of idle/working/blocked/done, upsert it on the caller's role, and mirror it to the `task_meta` "hera" namespace best-effort. When a WORKER role reports `done` the system SHALL roll its bound task to in_review and stamp `ready_to_close` via `RollHeraWorkerToReview` — the primary BUG-050 trigger for the idle-but-done case the exit hook misses. That roll is worker-kind only, no-ops unless the task is currently in_progress (so it never clobbers a human-set in_review/complete and never auto-completes), leaves the live session running, is idempotent, and is soft-fail (a failure never blocks the status update).

Derived from: `internal/mcp/hera.go:643` (`toolHeraStatus`), `internal/mcp/hera.go:691` (BUG-050 worker roll), `internal/tui/hera/ops.go:193` (the same roll mirrored on the rail `s` key).

#### Scenario: Invalid status is rejected

- **WHEN** hera_status is called with a status other than idle/working/blocked/done
- **THEN** the tool errors naming the valid values

#### Scenario: Worker done rolls to in_review

- **WHEN** a worker role reports status=done and its task is in_progress
- **THEN** the task is rolled to in_review and stamped ready_to_close, while the session keeps running

#### Scenario: Done roll never clobbers a non-progress task

- **WHEN** a worker reports done but its task is already in_review or complete
- **THEN** RollHeraWorkerToReview no-ops and the status update still succeeds

### Requirement: Subtree TLDR roll-up via hera_tree_updates

The system SHALL, on `hera_tree_updates`, scan the caller's orchestrator subtree for messages newer than a cursor and return TLDR-only subject lines (no bodies), capped at 200, with a `next_cursor` equal to the max id returned. The subtree is every orchestrator reachable from the caller's by multi-binding BFS: a child orchestrator hangs off a frontier when its live, non-archived coordinator role's task also holds a live binding under the frontier; archived orchestrators are excluded as descendants (the root is always included). The cursor is stored per-role and auto-advances unless the caller pins an explicit `since` (which overrides and does not advance the stored cursor).

Derived from: `internal/mcp/hera.go:797` (`toolHeraTreeUpdates`), `internal/db/hera_subtree.go:55` (`SubtreeOrchIDs` BFS), `internal/db/hera_subtree.go:125` (`HeraTreeUpdatesSince`), `internal/db/hera_subtree.go:33` (`HeraTreeUpdatesLimit` = 200).

`NOTE:` Native's subtree bridges ONLY through bindings with `ended_at IS NULL` (`workerTaskSet` also requires `r.Live`). Per `docs/RAIL-PARITY-ANALYSIS.md` (Gap #2), the plugin bridges on a coordinator's LATEST binding regardless of liveness (excluding only `reparented`/`user_deleted` end reasons), so native's tree is structurally narrower — it drops every bridge whose binding ended for a benign reason. This is a documented native-vs-plugin difference that persists even with complete data.

#### Scenario: TLDR-only roll-up with paging cursor

- **WHEN** hera_tree_updates is called with no `since`
- **THEN** it returns subject-line entries for new subtree messages (no bodies), advances the stored per-role cursor, and returns next_cursor

#### Scenario: Explicit since does not advance the cursor

- **WHEN** hera_tree_updates is called with an explicit `since`
- **THEN** it returns updates after that id but leaves the stored per-role cursor unchanged

#### Scenario: Archived orchestrators are pruned from the subtree

- **WHEN** an orchestrator on the bridging path is archived
- **THEN** it and its descendants drop out of the subtree scan

### Requirement: Full message bodies via hera_get_messages with subtree access scope

The system SHALL, on `hera_get_messages`, fetch full message bodies by id, restricting access to messages whose sender OR recipient role lives in the caller's orchestrator SUBTREE. An inaccessible or missing id SHALL get a per-id error field rather than a top-level error. Required args: `cwd`, `ids`.

Derived from: `internal/mcp/hera.go:905` (`toolHeraGetMessages`), `internal/mcp/hera.go:931` (subtree resolution), `internal/mcp/hera.go:1017` (`heraRoleInSubtree` access predicate).

#### Scenario: In-subtree message returns its body

- **WHEN** a requested message's sender or recipient is in the caller's subtree
- **THEN** the full body is returned

#### Scenario: Out-of-subtree id is denied per-id

- **WHEN** a requested id is outside the caller's subtree
- **THEN** that id gets an "access denied" error entry while other accessible ids still return

#### Scenario: Missing id is reported per-id

- **WHEN** a requested id does not exist
- **THEN** that id gets a "not found" error entry rather than failing the whole call

### Requirement: Daemon-startup binding reconciliation

The system SHALL, on daemon startup, walk every live binding and end those whose argus task row no longer exists, stamping `end_reason="task_missing"`. This is keyed on task-row existence (not session liveness), so a task deleted while the daemon was down has its orphaned bindings cleaned up. The sweep is idempotent and safe to run on every boot; a transient lookup error leaves the binding live for a later boot to retry.

Derived from: `internal/heraadopt/heraadopt.go:34` (`ReconcileBindings`).

`NOTE:` The former auto-adopt watcher (Milestone 4 rule D4 — adopting a `depends_on`-linked task under a coordinator as a worker) was RETIRED with the `depends_on` DAG. Born-bound workers create their bindings transactionally at spawn time, so there is no link to adopt across. Only this startup reconciliation survives.

#### Scenario: Orphaned binding is ended on boot

- **WHEN** a live binding's argus task row no longer exists at daemon startup
- **THEN** the binding is ended with end_reason="task_missing"

#### Scenario: Transient error leaves the binding live

- **WHEN** the task lookup returns a transient (non-not-found) error
- **THEN** the binding is left live for a later boot to re-sweep
