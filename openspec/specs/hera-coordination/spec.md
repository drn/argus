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
- **THEN** it may also hold a live binding under orchestrator B (the constraint is per-orchestrator, not global) — reached only via `hera_new_orchestrator` self-promotion (the worker-promotion/subcoord pattern); neither `hera_join` nor `hera_move` ever leaves a task bound under two orchestrators at once (see their respective requirements)

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

### Requirement: Caller role resolution from cwd with orchestrator disambiguation

The system SHALL resolve the calling agent's hera role by mapping `cwd` to an argus task (subject to the shared-worktree disambiguation described under the `mcp-server` capability's "Caller identity resolved by id or cwd" requirement), then to that task's live binding+role. When the task holds exactly one live binding the `orchestrator` argument is optional; when it holds two or more, the tool SHALL return a disambiguation error listing the caller's orchestrator names, and the caller MUST re-call with `orchestrator=<name>`. An unbound task SHALL get an error instructing it to call `hera_join` or `hera_new_orchestrator`.

Binding resolution SHALL key first on the resolved task's `argus_task_id` and, on a miss, fall back to a lookup keyed on the task's `worktree_path` (scoped to the same orchestrator when one is given) — the same key the live-binding uniqueness index is defined on (BUG-059). This closes the gap where a `cwd` resolves to a task whose id has no live binding even though the binding physically rooted at that worktree is live: the worktree-keyed fallback resolves the exact binding an attach `hera_join` would collide with, so a claim now succeeds precisely when an attach would have been rejected. The fallback never rewrites the resolved binding's `argus_task_id` — it only returns the existing binding's identity; reconciling a binding whose own `argus_task_id` has drifted is the separate `hera_rebind` tool.

Derived from: `internal/mcp/hera.go:398` (`resolveCallerRole`), `internal/mcp/hera.go:457` (`liveBindingForOrch`), `internal/mcp/hera.go:478` (`liveBindingForTask`), `internal/mcp/hera.go:494` (`buildHeraAmbiguousError`).

#### Scenario: Single binding resolves without orchestrator

- **WHEN** the caller's task holds exactly one live binding and no `orchestrator` is passed
- **THEN** the role resolves successfully

#### Scenario: Ambiguous binding lists options

- **WHEN** the caller's task holds two or more live bindings and no `orchestrator` is passed
- **THEN** the tool errors with the list of bound orchestrator names to disambiguate

#### Scenario: Unbound task is guided

- **WHEN** the caller's task holds no live binding
- **THEN** the tool errors instructing it to call hera_join or hera_new_orchestrator

#### Scenario: Worktree-keyed fallback resolves a binding the task-keyed lookup missed

- **WHEN** the resolved task's id has no live binding under the given orchestrator, but a live binding for that orchestrator exists at the task's `worktree_path`
- **THEN** the role resolves to that worktree-keyed binding instead of reporting no binding exists

### Requirement: hera_new_orchestrator bootstraps and claims the coordinator role

The system SHALL, on `hera_new_orchestrator`, create (or idempotently fetch) the named orchestrator, then transactionally create a coordinator role bound to the calling task's worktree, persisting the caller's argus project on the role. It SHALL reject the call when the calling task already holds a live binding under that (target) orchestrator, directing the caller to `hera_join`. It SHALL ALSO reject the call when a live binding already occupies the caller's `worktree_path` under that orchestrator even if the task-keyed check above found nothing — a reused worktree path can leave the caller's cwd resolving to a task whose id has no binding while the physical worktree does (BUG-059) — directing the caller to `hera_join` to claim it, or `hera_rebind` when that existing binding's `argus_task_id` differs from the caller's resolved task. It SHALL ALSO reject the call — before creating or fetching the orchestrator, so no orphan orchestrator is left behind — when the calling task already holds a live **coordinator**-kind binding under a DIFFERENT orchestrator: a coordinator dispatches work with `hera_spawn_worker` (whose `project=` targets any repo) and MUST NOT bind its own session as a second coordinator of another orchestrator, so the rejection error SHALL direct the caller to spawn a worker, or — for genuine multi-project/multi-phase decomposition — to use the worker-promotion pattern or a `kind=subcoord` plan node. A caller re-calling for the SAME orchestrator it already coordinates falls through to the target-orchestrator rejection above (the `hera_join` guidance); a caller holding only worker/freelance bindings (worker self-promotion) or no binding at all (fresh bootstrap) SHALL be allowed to proceed. It SHALL mirror the role to the `task_meta` "hera" namespace best-effort (a mirror failure never undoes local state). Required args: `cwd`, `name`, `coordinator_role_name`.

Derived from: `internal/mcp/hera.go:511` (`toolHeraNewOrchestrator`).

#### Scenario: First call creates orchestrator + coordinator binding

- **WHEN** a task with no binding under orchestrator X calls hera_new_orchestrator(name=X)
- **THEN** orchestrator X exists, a coordinator role bound to the caller is created, and the binding id is returned

#### Scenario: Re-bootstrap under an already-bound orchestrator is rejected

- **WHEN** the caller already holds a live binding under the target orchestrator
- **THEN** the tool errors and directs the caller to hera_join

#### Scenario: A coordinator cannot create another orchestrator on its own session

- **WHEN** a task that already holds a live coordinator binding under orchestrator A calls hera_new_orchestrator(name=B) for a different orchestrator B
- **THEN** the tool errors, no orchestrator B and no second coordinator binding are created on that task, and the error directs the caller to hera_spawn_worker (or a kind=subcoord plan node for a real sub-team)

#### Scenario: Worker self-promotion to sub-coordinator is still allowed

- **WHEN** a task holding only a live worker binding calls hera_new_orchestrator(name=B)
- **THEN** orchestrator B and a coordinator role bound to the caller are created (the worker-promotion pattern succeeds)

#### Scenario: A worktree collision under a drifted task id is rejected with an actionable message

- **WHEN** the caller's `worktree_path` already holds a live binding under the target orchestrator whose `argus_task_id` differs from the caller's resolved task id
- **THEN** the tool errors directing the caller to `hera_join` to claim it (or `hera_rebind` to reconcile the drift) instead of the role+binding insert surfacing a raw uniqueness-constraint error

### Requirement: hera_join claims an existing role or attaches a new one

The system SHALL support two `hera_join` modes. With `role_name` omitted (claim mode) it SHALL return the caller's existing binding for the resolved orchestrator plus its unread message count, without cancelling any pending doorbell deliveries. With `role_name` + `kind` supplied (attach mode) it SHALL create a new role+binding of kind `worker` or `freelance` (rejecting `coordinator` — that path is `hera_new_orchestrator`), persisting the attaching task's project, optionally setting an initial status, and rejecting a second binding under an orchestrator the task is already bound to — whether that existing binding is found by the caller's resolved task id OR by the caller's `worktree_path` (BUG-059: a reused worktree can leave the task-keyed check blind to a binding that is nonetheless live at that physical location). When the collision is detected via the worktree-keyed check and the existing binding's `argus_task_id` differs from the caller's resolved task, the rejection SHALL additionally point at `hera_rebind` to reconcile the drift. Attach mode SHALL ALSO reject the call — creating no binding — when the calling task already holds a live binding under a DIFFERENT orchestrator than the one being joined, directing the caller to the `hera_move` tool instead: joining is for an unbound task or a fresh orchestrator, not for relocating an existing membership. Attach mode requires `orchestrator`.

Derived from: `internal/mcp/hera.go:640` (`toolHeraJoin`).

#### Scenario: Claim mode returns the existing binding

- **WHEN** a bound task calls hera_join with no role_name
- **THEN** the response reports its orchestrator, role name, kind, binding id, and unread count, and does NOT cancel pending deliveries

#### Scenario: Attach mode rejects coordinator kind

- **WHEN** hera_join is called in attach mode with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator

#### Scenario: Attach mode creates a worker/freelance role

- **WHEN** an unbound task calls hera_join with orchestrator, role_name, and kind=worker
- **THEN** a new worker role+binding is created and the binding id is returned

#### Scenario: Same-orchestrator conflict is unaffected

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting orchestrator A again
- **THEN** the tool errors as before, directing the caller to hera_join without role_name, and no binding is ended or created

#### Scenario: Attach mode rejects and redirects when bound under a different orchestrator

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting a different orchestrator B
- **THEN** the tool errors, directs the caller to call hera_move instead, and neither ends the binding under A nor creates one under B

#### Scenario: Claim mode resolves the live task despite a stale worktree collision

- **WHEN** hera_join claim mode is called with a cwd whose worktree is shared by a stale/archived task (holding no live binding) and the live task actually bound to the target orchestrator
- **THEN** the response reports the live binding's role identity, not an error saying no binding exists

#### Scenario: Attach mode rejects a worktree collision without leaking a raw constraint error

- **WHEN** hera_join attach mode is called and a live binding for the target orchestrator already exists at the caller's worktree_path, even though no binding is found for the caller's resolved task id
- **THEN** the tool errors with an actionable message (claim it via hera_join, or hera_rebind if the existing binding's argus_task_id has drifted) and never surfaces a raw uniqueness-constraint error

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

The system SHALL, on `hera_spawn_worker`, require the caller to hold a live COORDINATOR binding and create a new argus task (worktree + session) plus, transactionally, a worker role+binding pre-bound to it. The role+binding write is an `AfterPersist` hook inside `agent.CreateAndStart`, joining its LIFO compensating-cleanup stack so any failure unwinds every prior step. The worker's project defaults to the COORDINATOR'S OWN TASK project (authoritative, not `role.ArgusProject`). The role name defaults to a slug of the prompt and is uniquified within the orchestrator. An orientation prefix naming the coordinator + orchestrator is prepended to the delivered prompt; the verbatim prompt is also stored on the role. An optional per-worker `model` is passed through. An optional per-worker `archetype` is passed through to `agent.CreateAndStart` and persisted on the spawned task (and mirrored on the role); when omitted, the worker defaults to the `code_slice` archetype. Required args: `cwd`, `prompt`.

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

### Requirement: hera_status updates role status and rolls a finished worker

The system SHALL, on `hera_status`, validate the status as one of idle/working/blocked/done/failed, upsert it on the caller's role, and mirror it to the `task_meta` "hera" namespace best-effort. When a WORKER role reports `done` the system SHALL roll its bound task to in_review and stamp `ready_to_close` via `RollHeraWorkerToReview` — the primary BUG-050 trigger for the idle-but-done case the exit hook misses. That roll is worker-kind only, no-ops unless the task is currently in_progress (so it never clobbers a human-set in_review/complete and never auto-completes), leaves the live session running, is idempotent, and is soft-fail (a failure never blocks the status update).

`hera_status` SHALL additionally accept two optional parameters, valid for a caller of ANY hera-bound role kind (coordinator, worker, or freelance): `handoff_note` (a short free-text string) and `request_recycle` (a boolean). When `handoff_note` is supplied, the system SHALL overwrite `task_meta` (namespace `hera`, key `handoff_note`) with it in the same call, regardless of the caller's role kind. When `request_recycle` is `true`, the system SHALL record a pending-recycle intent for the caller's task, regardless of the caller's role kind, which the `recycle_coord` primitive (see `coordinator-context-management`) consumes to defer the actual restart until the session is idle.

Derived from: `internal/mcp/hera.go:643` (`toolHeraStatus`), `internal/mcp/hera.go:691` (BUG-050 worker roll), `internal/tui/hera/ops.go:193` (the same roll mirrored on the rail `s` key).

#### Scenario: Invalid status is rejected

- **WHEN** hera_status is called with a status other than idle/working/blocked/done/failed
- **THEN** the tool errors naming the valid values

#### Scenario: Worker done rolls to in_review

- **WHEN** a worker role reports status=done and its task is in_progress
- **THEN** the task is rolled to in_review and stamped ready_to_close, while the session keeps running

#### Scenario: Done roll never clobbers a non-progress task

- **WHEN** a worker reports done but its task is already in_review or complete
- **THEN** RollHeraWorkerToReview no-ops and the status update still succeeds

#### Scenario: A coordinator can record a handoff note

- **WHEN** a coordinator calls hera_status with a non-empty handoff_note
- **THEN** task_meta (hera, handoff_note) is overwritten with that text in the same call

#### Scenario: A coordinator can request recycle

- **WHEN** a coordinator calls hera_status with request_recycle=true
- **THEN** a pending-recycle intent is recorded for the caller's task

#### Scenario: A worker can record a handoff note and request recycle

- **WHEN** a worker role calls hera_status with a non-empty handoff_note and request_recycle=true
- **THEN** task_meta (hera, handoff_note) is overwritten with that text, and a pending-recycle intent is recorded for the caller's task, in the same call

#### Scenario: A freelance role can record a handoff note and request recycle

- **WHEN** a freelance role calls hera_status with a non-empty handoff_note and request_recycle=true
- **THEN** task_meta (hera, handoff_note) is overwritten with that text, and a pending-recycle intent is recorded for the caller's task, in the same call

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

### Requirement: Enter refuses to restart a dead-session worker awaiting close-out

Pressing `Enter` on a DEAD session (no live process at all) SHALL start it via the ordinary dead-session restart path (`startSession`) for a coordinator role, UNCHANGED. For a worker or freelance role, the system SHALL first check the SAME `HeraWorkerAwaitingCloseout` predicate the `Worker revive restores in_progress` requirement's guard uses (`meta:hera.ready_to_close`, or a terminal `done`/`failed` role-status) and, if the task is awaiting close-out, SHALL refuse to restart the session on the first two Enter presses: the task's status is left completely unchanged and no session is started. This check and its refusal behavior SHALL be identical whether the task is reached via the Hera tab's rail row or the plain Tasks tab's agent view — the same underlying task refuses the same way regardless of which surface it is viewed from.

Refusal SHALL toggle a persistent, in-pane banner on the agent pane bound to the task (whichever pane that is for the surface in use), rather than relying solely on a footer notice: the FIRST `Enter` press against a closed-out task SHALL arm the banner (a status-bar message is also shown, for continuity with the pre-existing behavior) — the banner SHALL replace whatever the pane would otherwise render (its dead-session replay content, or the "Session not running" placeholder) for as long as it stays armed. A SECOND, immediately-following `Enter` press SHALL dismiss the banner, at which point the pane SHALL render exactly what an ordinary dead-session pane renders — its last recorded session output if any was logged, else the same placeholder — reusing the pane's existing dead-session rendering with no new PTY, process, or emulator spawned for this view. A THIRD, immediately-following `Enter` press SHALL actually revive the task: it SHALL clear both underlying close-out signals (`meta:hera.ready_to_close` and any terminal `done`/`failed` role-status on a live binding) and then start the session via the ordinary dead-session restart path, exactly as if no close-out marker had ever been present — this is a deliberate operator override of the guard, not a call to the stricter `Worker revive restores in_progress` guard (which continues to refuse this case in its own, separate contexts). The banner state (including how many of the three steps have been taken) is scoped to the pane's current task binding and SHALL reset — restarting the sequence at the first step — whenever the pane is rebound to a different task and back, including a rebind to the SAME task after navigating away.

Whichever surface the task is viewed from, the auto-start path that would otherwise restart a dead session merely on navigating to it (e.g. the plain Tasks tab's task-selection flow) SHALL also respect this guard: it SHALL skip starting the session for a closed-out task without arming the banner (mere navigation is not an `Enter` press), leaving the pane to show its ordinary dead-session view until the operator presses `Enter` explicitly.

This closes a gap discovered by live-testing `hera_accept`: the dead-session branch previously called `startSession` unconditionally for every role kind, which unconditionally flips the task to in_progress with zero Hera awareness. Because the underlying session had nothing left to resume, it exited almost immediately, and the ordinary post-exit rule then rolled the task to in_review – silently undoing an explicit `hera_accept` (or a self-reported-done worker's `ready_to_close` stamp) even though `Enter` is not itself an explicit revive. The refusal makes the "a premature accept can only be undone via an explicit revive" guarantee (`hera_accept`'s own tool description) hold for every UI trigger, not only the two that happened to call `ReviveHeraWorkerToInProgress` already (the live-session kick and `hera_revive`). The in-pane banner (`add-hera-closeout-banner`) closes a SEPARATE, UX-only gap: the original fix's only feedback was a 15-second-TTL status-bar notice, easy to miss, that left the pane's own content exactly as it already was — never actually showing anything or letting the operator proceed. `add-enter-closeout-guard-parity` closed a THIRD gap: the guard and banner originally protected only the Hera tab, leaving the plain Tasks tab's own Enter-to-restart and auto-start paths completely unguarded for the SAME underlying task. `add-force-revive-third-enter` then reversed the original "no separate third state" decision after dogfood testing found it unhelpful: three deliberate Enter presses in a row is unambiguous operator intent to reopen the task, not an accidental repeat.

Derived from: `internal/tui/heraactions.go` (`heraReattach`, `heraTaskClosedOut`, `heraReattachClosedOut`, `heraKickRestartClosedOut`), `internal/tui/app.go` (`reattachClosedOut`, `forceReviveClosedOut`, `onTaskSelect`, `handleAgentKey`), `internal/db/hera.go` (`HeraWorkerAwaitingCloseout`, `ClearHeraCloseout`), `internal/tui/terminal/terminalpane.go` (`ShowClosedOutBanner`/`DismissClosedOutBanner`/`ClosedOutBannerShown`/`ClosedOutReadyToRevive`/`ClearClosedOutState`).

#### Scenario: Enter on a dead-session accepted (complete) worker is refused and arms the banner

- **WHEN** a worker's task is `complete` (accepted via `hera_accept`) and has no live session, and the operator presses `Enter` on it for the first time this visit, from either the Hera tab or the plain Tasks tab
- **THEN** no session is started, the task's status stays `complete`, the status bar shows a closed-out message, and the bound agent pane shows the persistent closed-out banner instead of its replay content or placeholder

#### Scenario: Enter on a dead-session self-reported-done worker is refused and arms the banner

- **WHEN** a worker's task carries `meta:hera.ready_to_close` (or a terminal `done`/`failed` role-status) and has no live session, and the operator presses `Enter` on it for the first time this visit
- **THEN** no session is started, the task's status is left unchanged, and the bound agent pane shows the closed-out banner

#### Scenario: A second, immediately-following Enter dismisses the banner and shows read-only output

- **WHEN** the closed-out banner is currently armed on the agent pane and the operator presses `Enter` again
- **THEN** no session is started, the banner is dismissed, and the pane renders its last recorded session output read-only (or the "Session not running" placeholder if none was ever recorded) — reusing the pane's existing dead-session rendering, spawning no new PTY, process, or emulator

#### Scenario: A third Enter actually revives the task

- **WHEN** the closed-out banner has been dismissed on the agent pane and the operator presses `Enter` again
- **THEN** the task's `meta:hera.ready_to_close` mark is cleared, any terminal `done`/`failed` role-status on its live binding is reset to `working`, and the session is started via the ordinary dead-session restart path — the banner does NOT re-arm

#### Scenario: Leaving and returning to the row resets the sequence

- **WHEN** the operator navigates away from a closed-out task (rebinding its agent pane to a different task or unbinding it) and then back to the SAME task
- **THEN** the banner is not armed on return and the next `Enter` press is treated as the FIRST step again (arming the banner), not as a continuation of a prior visit's progress toward a revive

#### Scenario: Enter still restarts a dead session with no close-out marker

- **WHEN** a worker or freelance task has no live session and carries no close-out marker
- **THEN** `Enter` restarts it exactly as before this change, and no closed-out banner is ever shown

#### Scenario: Coordinators are unaffected

- **WHEN** a coordinator role's dead session is reattached via `Enter`, from either tab
- **THEN** it is restarted unconditionally, exactly as before this change – coordinators have no close-out concept and never show the closed-out banner

#### Scenario: Mere navigation on the plain Tasks tab does not auto-restart a closed-out task

- **WHEN** the operator selects a closed-out worker/freelance task from the plain Tasks tab (no live session), without yet pressing `Enter`
- **THEN** the auto-start path SHALL skip starting the session, the banner SHALL NOT be armed, and the pane SHALL show its ordinary dead-session view until the operator explicitly presses `Enter`

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

### Requirement: hera_move relocates the caller's binding to a different orchestrator

The system SHALL, on `hera_move`, relocate the calling task's live hera binding to a different orchestrator: it SHALL resolve the caller's current live binding (via the same cwd→task→binding resolution used elsewhere, accepting an optional `from_orchestrator` to disambiguate when the task holds 2+ live bindings), then — transactionally — end that binding (`ended_at`/`end_reason: "moved"`) and create a new role+binding of kind `worker` or `freelance` under the target `orchestrator` (rejecting `coordinator`, mirroring `hera_join`). It SHALL reject the call, ending and creating nothing, when the calling task holds no live binding at all (directing the caller to `hera_join` or `hera_new_orchestrator` instead — there is nothing to move), when the resolved source orchestrator equals the target orchestrator (a no-op; directing the caller to `hera_join` without `role_name` to see its current binding), or when the resolved SOURCE binding's role is coordinator-kind (a coordinator's binding IS its orchestrator's coordination — ending it would orphan the whole subtree the coordinator was running, leaving a disconnected worker/freelance stub under the target with no structural link back; the rejection names the caller's role and orchestrator and directs the caller to ask a human to use the Hera TUI's `J` adopt/reparent key instead, since no agent-facing tool nests an existing coordinator + subtree under a new parent). The response SHALL report the source orchestrator and role name that were moved, plus the new binding id. Required args: `cwd`, `orchestrator`, `role_name`, `kind`. Optional args: `from_orchestrator`, `status`.

#### Scenario: Happy path moves the binding

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator B with a role_name and kind
- **THEN** the binding under A is ended with end_reason "moved", a new role+binding is created under B, and the response reports A + the moved role's name plus the new binding id

#### Scenario: Nothing to move

- **WHEN** an unbound task calls hera_move
- **THEN** the tool errors that there is nothing to move and directs the caller to hera_join or hera_new_orchestrator, creating no binding

#### Scenario: Moving to the same orchestrator is a no-op error

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator A
- **THEN** the tool errors and directs the caller to hera_join without role_name, ending and creating nothing

#### Scenario: Ambiguous caller requires from_orchestrator

- **WHEN** a task holding live bindings under two orchestrators calls hera_move without from_orchestrator
- **THEN** the tool errors listing the bound orchestrator names, and a follow-up call supplying from_orchestrator succeeds

#### Scenario: Destination kind=coordinator is rejected

- **WHEN** hera_move is called with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator

#### Scenario: A live coordinator's own binding cannot be moved

- **WHEN** a task holding a live COORDINATOR binding under orchestrator A calls hera_move targeting orchestrator B with kind=worker or kind=freelance
- **THEN** the tool errors identifying the caller as orchestrator A's coordinator and directing them to the Hera TUI's `J` adopt/reparent key, and the original coordinator binding under A remains live and unchanged, with no role or binding created under B

### Requirement: hera_rebind reconciles a stuck or drifted binding

The system SHALL provide `hera_rebind(cwd, orchestrator, [role_name])` as the supported repair path for a hera binding stuck in the claim-says-none / attach-says-exists state (BUG-059: a worktree path reused across two argus tasks' lifecycles left the live binding pointing at a stale `argus_task_id`, so delivery and status routing — which key on the binding's `argus_task_id` — go nowhere even after the task-then-worktree lookup fallback). `hera_rebind` SHALL reconcile the binding to the caller's real live argus task WITHOUT tearing down the argus session: it reuses the existing role (so the role's prompt, messages, and status, all keyed on `role_id`, survive) and refreshes only the binding row.

`hera_rebind` SHALL resolve the caller's real live task from `cwd` (using the shared-worktree disambiguation specified under the `mcp-server` capability), then gather the caller's live bindings under the named orchestrator keyed both by the resolved task id and by the worktree path (de-duplicated). It SHALL pick the keeper role from those candidates: `role_name`, when given, MUST name a role holding one of the candidate bindings; otherwise exactly one role MUST be represented among the candidates. When the keeper binding already points at the caller's task and worktree and is the sole occupant of both slots, the call SHALL be a no-op that reports success without changing any row. Otherwise it SHALL end the keeper's stale binding and insert one clean binding under the keeper role at the caller's `argus_task_id` and `worktree_path`, and SHALL best-effort mirror `meta:hera.role` to the caller's task.

`hera_rebind` SHALL REFUSE (return a tool error, change no rows) rather than guess when the state is genuinely ambiguous: the cwd itself is ambiguous (two live `in_progress` tasks share the worktree); no candidate live binding exists to reconcile (directing the caller to `hera_join` instead — `hera_rebind` repairs, it does not create); more than one role holds a candidate binding and no `role_name` disambiguates; or a DIFFERENT role's live binding already occupies the caller's target task or worktree slot.

Derived from: `internal/mcp/hera.go:901` (`toolHeraRebind`).

#### Scenario: Rebind reconciles a drifted binding to the caller's live task

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and the live binding for X at the caller's worktree points at a stale argus_task_id while the caller's cwd resolves unambiguously to a different, real live task
- **THEN** the stale binding is ended, a clean binding is created under the SAME role pointing at the caller's argus_task_id and worktree_path, both the task-keyed and worktree-keyed lookups now resolve that one binding, and the role's messages survive

#### Scenario: Rebind is a no-op when the binding is already consistent

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and the live binding already points at the caller's task and worktree and is the sole occupant of both slots
- **THEN** the response reports success without changing any binding row

#### Scenario: Rebind refuses an ambiguous cwd

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and two live in_progress argus tasks share the caller's worktree
- **THEN** the tool errors naming the candidates and changes no binding row

#### Scenario: Rebind refuses when multiple roles are bound and no role_name is given

- **WHEN** hera_rebind(cwd, orchestrator=X) is called, the caller's task and worktree resolve to bindings held by DIFFERENT roles under X, and no role_name is supplied
- **THEN** the tool errors listing the candidate roles and directing the caller to pass role_name, and changes no binding row

#### Scenario: Rebind refuses when there is nothing to reconcile

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and no live binding for X exists at the caller's worktree or task
- **THEN** the tool errors directing the caller to hera_join, and creates no binding

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

