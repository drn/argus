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

The system SHALL register twelve native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, and the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

#### Scenario: Tools require task management

- **WHEN** task management is disabled
- **THEN** the `hera_*` tools report "hera not configured" rather than acting

#### Scenario: Native and plugin hera tools are mutually exclusive

- **WHEN** native Hera is enabled
- **THEN** any plugin tool scoped `hera` is suppressed so only the in-tree tools appear

#### Scenario: Plan-authoring tools are coordinator-only

- **WHEN** a worker or freelance role calls `hera_plan_node`, `hera_block`, or `hera_plan`
- **THEN** the tool errors that only coordinators may author the plan

#### Scenario: Whole-graph submission in one call

- **WHEN** a coordinator calls `hera_plan` with a set of nodes and blocking edges
- **THEN** the planned nodes and their cycle-checked edges are created together

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

The system SHALL, on `hera_tree_updates`, scan the caller's orchestrator subtree for messages newer than a cursor and return TLDR-only subject lines (no bodies), capped at 200, with a `next_cursor` equal to the max id returned. The subtree is every orchestrator reachable from the caller's by multi-binding BFS: a child orchestrator hangs off a frontier when its non-archived coordinator role's task ALSO has its LATEST binding under the frontier — regardless of that binding's liveness — unless that latest binding ended via an operator teardown (`reparented`/`user_deleted`), which severs the link; archived orchestrators are excluded as descendants (the root is always included). The cursor is stored per-role and auto-advances unless the caller pins an explicit `since` (which overrides and does not advance the stored cursor).

Derived from: `internal/mcp/hera.go:797` (`toolHeraTreeUpdates`), `internal/db/hera_subtree.go:55` (`SubtreeOrchIDs` BFS), `internal/db/hera_subtree.go:125` (`HeraTreeUpdatesSince`), `internal/db/hera_subtree.go:33` (`HeraTreeUpdatesLimit` = 200).

`NOTE:` Native's subtree bridges through each coordinator role's LATEST binding regardless of liveness — the `latest` CTE in `internal/db/hera_subtree.go` selects the max binding id per role, so a child whose coordinator session has finished still nests under its parent. Only a latest binding ended via an operator teardown (`reparented`/`user_deleted`) severs the link; every other end reason leaves the structural bridge intact. Since the #747 rail-parity rewrite this matches the plugin's bridging (per `docs/RAIL-PARITY-ANALYSIS.md`, Gap #2) — native is no longer structurally narrower.

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

### Requirement: Plan-authoring tools accept a sub-coordinator node kind and goal

The plan-authoring tools `hera_plan_node` and `hera_plan` SHALL accept an optional node **kind** parameter with values `worker` (the default) and `subcoord`, and for a `subcoord` node SHALL accept a **goal** prompt and nothing further — the tools SHALL NOT accept a child-orchestrator name or coordinator-role name (the parent hands only the goal; those are derived at materialize time and the sub-coordinator owns its plan). When the kind is omitted or `worker`, the tools SHALL create a leaf-worker planned node exactly as before. When the kind is `subcoord`, the tools SHALL create a planned node carrying the `subcoord` discriminator and goal (for the gater's coordinator materialize path), rejecting the request when the goal is absent. The coordinator-only guard SHALL apply to `subcoord` nodes identically to worker nodes — a worker or freelance caller SHALL be rejected. In `hera_plan`'s whole-graph submission, individual nodes SHALL be independently typeable as `worker` or `subcoord`, and a blocking edge MAY reference a `subcoord` node on either endpoint.

#### Scenario: hera_plan_node accepts a sub-coordinator node

- **WHEN** a coordinator calls `hera_plan_node` with kind `subcoord` and a goal
- **THEN** a planned node carrying the `subcoord` discriminator and goal is created

#### Scenario: Sub-coordinator node requires a goal

- **WHEN** a coordinator calls `hera_plan_node` or `hera_plan` with a `subcoord` node that has no goal
- **THEN** the tool rejects the request with an error that a sub-coordinator node requires a goal

#### Scenario: Omitted kind creates a leaf worker

- **WHEN** a coordinator authors a node without specifying a kind
- **THEN** a leaf-worker planned node is created, unchanged from the substrate

#### Scenario: Whole-graph submission mixes node kinds

- **WHEN** a coordinator calls `hera_plan` with some `worker` nodes and some `subcoord` nodes connected by blocking edges
- **THEN** each node is created with its specified kind and the cycle-checked edges are created together

#### Scenario: Non-coordinator cannot author a sub-coordinator node

- **WHEN** a worker or freelance role calls `hera_plan_node` or `hera_plan` to create a `subcoord` node
- **THEN** the tool errors that only coordinators may author the plan

### Requirement: Two-state end-of-life representation (hidden vs nuked) in the store

The hera store SHALL represent two distinct, additive end-of-life states on BOTH `hera_orchestrators` and `hera_roles`, layered on the existing `archived_at` marker with a second nullable `nuked_at TEXT` column:

- **ACTIVE** — `archived_at IS NULL AND nuked_at IS NULL`.
- **HIDDEN (Tier 1)** — `archived_at` set, `nuked_at IS NULL`. The row is reversibly archived; the rail nests it in the parent coordinator's archive expando. Clearing `archived_at` (unhide) restores it.
- **NUKED (Tier 2)** — `nuked_at` set (and `archived_at` set, so the row also leaves the partial active-name unique index and frees its name for reuse). The rail omits the row entirely; the row, its inbox, and its argus task remain retrievable from the DB.

The store SHALL expose `NukeHeraRole(id)` and `NukeHeraOrchestrator(id)` that stamp `nuked_at` (idempotently, preserving an existing value) and ensure `archived_at` is set, clearing `pinned_at`. Every list/lookup that feeds the rail (`ListHeraOrchestrators`, `ListHeraRoles`) SHALL exclude `nuked_at`-stamped rows; primary-key lookups by id MAY still return them (so recovery tooling can read them). The active-name uniqueness lookups SHALL treat a nuked row as not occupying the name (it is archived).

This representation is the storage substrate for the Hera-view two-state EOL keys (`a` hide, `Ctrl+D`/`C` nuke). NO end-of-life path SHALL hard-delete a hera row: nuke stamps `nuked_at`, it never calls `DeleteHeraRole` / `DeleteHeraOrchestrator`, and message rows are never deleted.

Derived from: `internal/db/schema.go` (`nuked_at` column + idempotent ALTER), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`, nuked-aware list/scan), `internal/tui/hera/model.go` (BuildModel skips rows with `nuked_at` set).

#### Scenario: Nuke stamps the marker and frees the name

- **WHEN** `NukeHeraRole` is called on an active role
- **THEN** the role's `nuked_at` and `archived_at` are stamped, its `pinned_at` cleared, and a later `CreateHeraRole` with the same (orchestrator, name) succeeds because the nuked row no longer occupies the active-name index

#### Scenario: Rail-feeding lists exclude nuked rows but id lookups do not

- **WHEN** an orchestrator or role is nuked
- **THEN** `ListHeraOrchestrators`/`ListHeraRoles` omit it (so BuildModel never renders it), while `HeraOrchestrator(id)`/`HeraRole(id)` still return it for recovery

#### Scenario: Nuke is reversible only via the DB, never a hard delete

- **WHEN** any end-of-life path nukes a row
- **THEN** the row is stamped (not deleted), its bindings/inbox/status/argus-task rows survive, and no `DeleteHeraRole`/`DeleteHeraOrchestrator`/message delete is issued

### Requirement: Worker and plan prompts carry the mission only, not injected policy

The system SHALL document, on the `prompt` parameter of `hera_spawn_worker` and
`hera_plan_node`, that the caller supplies the worker's MISSION/task only and does
NOT prepend organization or security policy into the prompt. The param
descriptions SHALL state the rationale: every spawned worker session receives its
organization instructions independently (harness-injected as an
`<organizationInstructions>` block), so a manually prepended policy is a redundant
copy that also pollutes the stored role prompt and the plan-DAG view.

argus SHALL continue to store the supplied `prompt` verbatim on the role
(unchanged) — this requirement governs the documented tool contract, not
enforcement: argus SHALL NOT parse, strip, or reject prompt content based on
"policy-looking" text.

Derived from: `internal/mcp/hera.go` (`hera_spawn_worker` tool registration +
`RolePrompt`), `internal/mcp/hera_plan.go` (`hera_plan_node` tool registration +
verbatim `Prompt`).

#### Scenario: spawn/plan prompt params document mission-only

- **WHEN** the `hera_spawn_worker` or `hera_plan_node` tool schema is inspected
- **THEN** the `prompt` param description directs supplying the worker's mission only
- **AND** it does not direct prepending organization/security policy, stating that the worker session receives org instructions independently

#### Scenario: The supplied prompt is still stored verbatim

- **WHEN** a coordinator calls `hera_spawn_worker` or `hera_plan_node` with any `prompt`
- **THEN** argus stores that prompt verbatim on the role, without stripping or transforming it

