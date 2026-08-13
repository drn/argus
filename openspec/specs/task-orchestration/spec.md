# Task Orchestration (RETIRED)

## Purpose

> **This capability is RETIRED.** Argus previously carried a declarative
> `depends_on` task-dependency DAG: an orchestrator agent built a directed
> acyclic graph where each task declared upstream dependencies, a dependency
> watcher auto-started blocked tasks once their deps completed, a halt cascade
> propagated across downstream tasks when a milestone failed, and the graph was
> laid out visually for inspection. It also owned the graph primitives —
> link/unlink mutations, cycle detection, and one-hop neighbour computation.
>
> All of it was removed in favor of **Hera** (coordinator-driven worker
> spawning) as the single orchestration model. Deleted: `model.Task.DependsOn`/
> `PlanSlug` and their DB columns, `internal/orch`, `internal/depswatcher`,
> `agent.StartPendingBlocked`, the `task_link`/`task_unlink`/`task_deps`/
> `task_halt_downstream`/`task_set_plan_slug` MCP + REST surface, the `/api/dag`
> endpoint, the standalone TUI DAG tab, and the SPA DAG view. Tasks now start
> immediately on creation; a coordinator sequences its workers itself and
> stacks PRs by branching each worker off the previous via `base_branch`
> (the git-stacking mechanic, which was KEPT).
>
> The live orchestration model is documented by the `task-messaging`,
> `mcp-server` (the `hera_*` tools), and `tui-shell` capabilities. The Hera view
> renders the **orchestration tree** (the coordinator → worker → sub-coordinator
> role-binding hierarchy), not a `depends_on` graph.
## Requirements
### Requirement: Planned node is a hera role with no live binding

The system SHALL allow a coordinator to create a **planned node**: a `hera_roles` row of kind `worker` that has **no live binding** and therefore no agent process, no worktree, and no inbox. A planned node SHALL persist its target argus project, the prompt to deliver on materialization, a planner-assigned short-id-prefixed name, and an optional archetype that is propagated onto the task at materialization. Creating a planned node SHALL be a coordinator-only operation; a worker or freelance caller SHALL be rejected. A planned node SHALL NOT trigger `agent.CreateAndStart` or any worktree creation at plan time.

#### Scenario: Coordinator creates a planned node

- **WHEN** a coordinator creates a planned node with a name, prompt, and project
- **THEN** a worker-kind role is persisted with no live binding, and no agent, worktree, or inbox is created

#### Scenario: Non-coordinator cannot plan

- **WHEN** a worker or freelance role attempts to create a planned node
- **THEN** the operation is rejected with an error that only coordinators may author the plan

#### Scenario: Planned node carries its materialization inputs

- **WHEN** a planned node is created
- **THEN** its target project, delivery prompt, short-id-prefixed name, and optional archetype are persisted on the role for use at materialization

#### Scenario: Planned archetype materializes onto the task

- **WHEN** a planned node carrying archetype `review` is materialized
- **THEN** the resulting task carries `review` as its archetype

### Requirement: Blocking edges between roles with cycle detection

The system SHALL store directed blocking edges in a `hera_blocks(blocked_role_id, blocker_role_id)` table with foreign keys to `hera_roles`. On inserting an edge the system SHALL run a depth-first cycle check within the same transaction and SHALL reject any edge that would create a cycle. Both endpoints of an edge SHALL belong to the same orchestrator; an edge whose endpoints are in different orchestrators SHALL be rejected (single-orchestrator scope for this version). The system SHALL reject an edge whose **blocker** endpoint is a `coordinator` role: a coordinator's session stays alive for the whole orchestration and never reaches role-status `done`, so gating a node on it is a permanently-unsatisfiable dependency; a clear creation-time error is returned rather than silently planning the dependent forever. The rejection is blocker-side only — a coordinator MAY be the blocked endpoint. When a blocker role no longer exists, the system SHALL treat the dependent as no longer blocked by that missing edge rather than failing.

#### Scenario: Cycle is rejected

- **WHEN** adding a blocking edge that would make the graph cyclic
- **THEN** the insert is rejected and the edge is not stored

#### Scenario: Coordinator-as-blocker edge is rejected

- **WHEN** a blocking edge is attempted whose blocker endpoint is a coordinator role
- **THEN** the insert is rejected with a coordinator-blocker error and the edge is not stored (a coordinator never reaches role-status done)

#### Scenario: Coordinator may be the blocked endpoint

- **WHEN** a blocking edge is attempted whose blocked endpoint is a coordinator role and whose blocker is a worker role
- **THEN** the insert is accepted (only coordinator-as-blocker is the never-satisfiable case)

#### Scenario: Cross-orchestrator edge is rejected

- **WHEN** a blocking edge is attempted between roles in two different orchestrators
- **THEN** the insert is rejected with a single-orchestrator-scope error

#### Scenario: Missing blocker is pruned, not fatal

- **WHEN** a dependent's blocker role has been removed
- **THEN** that edge is ignored and the dependent is treated as no longer blocked by it

### Requirement: Plan DAGs compose hierarchically through sub-coordinators

Each orchestrator SHALL own its own plan DAG (the blocking edges among its own roles). A sub-coordinator — a task holding a live worker binding in a parent orchestrator and a live coordinator binding in a child orchestrator — SHALL appear as a single node in the parent's DAG while owning a separate, independent DAG in its child orchestrator. Because cross-orchestrator edges are rejected, whole-phase sequencing across sub-teams SHALL be expressed at the parent level: an edge between the sub-coordinators' worker roles, which both belong to the parent orchestrator, where a sub-coordinator's worker-role status reaching `done` is the gate the parent's dependents wait on. Sibling sub-coordinators whose parent worker roles share no edge SHALL run as independent, unconnected DAGs. Adopting a coordinator that already owns a DAG SHALL leave that DAG intact in its own orchestrator and add it as a single node to the adopter's DAG, merging no edges across the boundary.

#### Scenario: Sub-coordinator is one node in the parent DAG

- **WHEN** a worker in a parent orchestrator is also the coordinator of a child orchestrator
- **THEN** it appears as a single node in the parent's DAG and owns a separate DAG in the child orchestrator

#### Scenario: Phase sequencing across sub-teams via the parent

- **WHEN** two sub-coordinators' parent worker roles are connected by a blocking edge
- **THEN** the dependent sub-coordinator materializes only after the blocker sub-coordinator's worker role reaches done

#### Scenario: Independent sibling sub-teams

- **WHEN** two sibling sub-coordinators have no edge between their parent worker roles
- **THEN** their DAGs are independent and run without connection

#### Scenario: Adopting a coordinator preserves its DAG

- **WHEN** a coordinator that owns a DAG is adopted as a worker under another coordinator
- **THEN** its DAG remains intact in its own orchestrator and the adopter's DAG gains it as a single node, with no edges merged across the boundary

### Requirement: Gater materializes a planned node when its blockers complete

The system SHALL run a hera-native gater that watches role status and, when **all** of a planned node's blockers have reached `done`, materializes that node into a live worker by creating its binding and agent via the existing `agent.CreateAndStart` against the **pre-created** role (not a freshly minted role). Materialization SHALL be idempotent: a node that is already bound, already materializing, or already `ready`-and-claimed SHALL NOT be materialized again. A node with any blocker not yet `done` SHALL remain planned. At materialization the worktree and branch SHALL be created, with `base_branch` resolved from the now-existing blocker branches; when a node has multiple blockers, `base_branch` SHALL be the branch of the most-recently-bound blocker (the stack tip). When a node has **two or more** blockers, materialization SHALL additionally notify the coordinator naming the chosen `base_branch` (and which blocker it came from) and the other blocker(s)' branches that were NOT merged in, so the pick is visible even when the coordinator did not author a self-rebase step. This notice is a one-shot, best-effort delivery: a delivery failure SHALL be logged and SHALL NOT be retried, and SHALL NOT affect materialization, which has already succeeded.

#### Scenario: All blockers done triggers materialization

- **WHEN** the last blocker of a planned node reaches done
- **THEN** the gater materializes the node into a live worker via CreateAndStart against the pre-created role

#### Scenario: Materialization is idempotent

- **WHEN** the gater re-evaluates a node that is already bound or already materializing
- **THEN** it does not spawn a second agent for that node

#### Scenario: Node stays planned while a blocker is unfinished

- **WHEN** at least one blocker of a planned node has not reached done
- **THEN** the node remains planned and no agent is spawned

#### Scenario: Worktree and base_branch resolved at materialization

- **WHEN** a planned node materializes
- **THEN** its worktree and branch are created at that moment and base_branch is resolved from its blockers' branches

#### Scenario: Fan-in materialization notifies the coordinator of the un-merged siblings

- **WHEN** a planned node with two or more blockers materializes
- **THEN** the coordinator is notified naming the resolved `base_branch` (and the blocker it came from) and the other blocker(s)' branches that were not automatically merged in

#### Scenario: Single-blocker and root materializations stay silent

- **WHEN** a planned node with zero or one blockers materializes
- **THEN** no fan-in notice is sent (there is no sibling branch to report)

#### Scenario: A failed fan-in notice does not affect materialization or retry

- **WHEN** the fan-in notice delivery fails
- **THEN** the failure is logged, the already-completed materialization is unaffected, and the notice is not retried on a later tick

### Requirement: Materialized worker checks in before doing real work

The system SHALL deliver to every gater-materialized worker a standing instruction, prepended to its prompt, to **check in with its coordinator and poll `hera_inbox`** for a go/wait decision before doing real work. The check-in SHALL be worker-pulled (the worker reads its inbox), never a push the worker waits for passively, because mid-flight pushed messages to a busy or freshly-started worker are unreliable. The daemon gate itself SHALL NOT consult the coordinator before spawning — the spawn decision is purely state-driven.

#### Scenario: Materialized worker boots with a check-in order

- **WHEN** the gater materializes a worker
- **THEN** the delivered prompt instructs it to check in with its coordinator and poll hera_inbox for go/wait before real work

#### Scenario: Go/wait arrives via pulled inbox

- **WHEN** the coordinator replies go or wait to a checked-in worker
- **THEN** the worker receives that decision by reading its inbox, not by waiting for a pushed delivery

#### Scenario: Spawn does not wait on the coordinator

- **WHEN** a planned node's blockers are all done
- **THEN** the daemon materializes it immediately without first asking the coordinator

### Requirement: Gate on role-status done; a failed blocker holds the dependent

The gate SHALL be the blocker's hera **role status** reaching `done` — the worker's explicit declaration that its work is finished. This is distinct from the argus **task status**: a finished hera worker rolls its task to `in_review` (never auto-`complete`), so task status is NOT the gate; only role-status `done` is. A blocker still `working` — for example iterating on CI by pushing PRs — SHALL NOT satisfy the gate, so the next phase does not start under churning work. When a blocker's session ends without its role ever reaching `done` (a crash or other failure), the system SHALL **hold** the dependent (not materialize it) and notify the coordinator, so no worker is spawned and parked behind dead or unfinished work. A blocker that has **never been bound** (a planned node still waiting on its own blockers) SHALL NOT be treated as failed: it has not started, so the dependent simply stays planned (pending), and the coordinator is NOT notified. The distinction is whether the blocker ever held a binding — never-bound is pending, bound-then-ended-without-done is failed. An **alive coordinator** blocker SHALL likewise NOT be treated as failed: a coordinator's session is alive for the whole orchestration and never reaches role-status `done`, so while its binding is live the dependent stays planned (pending) with no hold-notification — even though its bound task may have moved off `in_progress`. This gater guard is defense-in-depth for any coordinator-as-blocker edges already present in the store (new such edges are rejected at creation). This MUST hold transitively: in a chain A→B→C where A is still working and B is an unstarted planned node, C stays planned (it is not held behind B), so DAGs deeper than two levels resolve correctly. The hold-notification SHALL be sent once per held (dependent, blocker) pair rather than repeated on each evaluation, so the coordinator is alerted without being spammed.

#### Scenario: Only role-status done opens the gate

- **WHEN** every blocker of a node has hera role status `done`
- **THEN** the node materializes

#### Scenario: A still-working blocker keeps the dependent planned

- **WHEN** a blocker is still `working` (e.g. iterating on CI) and has not reported role-status `done`
- **THEN** the dependent stays planned and does not start

#### Scenario: A crashed or unfinished blocker holds and notifies

- **WHEN** a blocker's session ends without its role reaching `done`
- **THEN** the dependent is held (not materialized) and the coordinator is notified

#### Scenario: A never-started planned blocker keeps the dependent planned, not held

- **WHEN** a node's blocker is itself an unstarted planned node (never bound) — for example in a chain A→B→C where A is still working and B has not yet materialized
- **THEN** the dependent (C) stays planned (pending), is not held, and the coordinator is not notified — a planned blocker is not a failed blocker, so transitive DAGs deeper than two levels resolve correctly

#### Scenario: An alive coordinator blocker keeps the dependent planned, not held

- **WHEN** a node's blocker is a coordinator role whose binding is still live (its session has not ended) and whose role status is not `done` — even if its bound task has moved off `in_progress`
- **THEN** the dependent stays planned (pending), is not held, and the coordinator is not notified — an alive coordinator never reaches `done` but has not failed, so it is not a failed blocker

### Requirement: Planner-assigned stable short-id naming

The system SHALL accept a planner-assigned short-id prefix on a node's name, by convention `<stage-number><parallel-letter>` (e.g. `2c-fact-checker`), where the number orders serial stages and the letter enumerates parallel members. The short-id SHALL be stable across plan edits (it is a durable handle, not recomputed). Rendering the short-id in the orchestration tree is out of scope for this version.

#### Scenario: Short-id prefix is accepted and persisted

- **WHEN** a planned node is created with a `<number><letter>-<slug>` name
- **THEN** the short-id-prefixed name is persisted as the node's stable handle

#### Scenario: Short-id is not recomputed on edit

- **WHEN** the plan is edited such that a node's effective stage would change
- **THEN** the node's assigned short-id remains unchanged

### Requirement: Plan-DAG root nodes materialize off a configurable base branch

When the gater materializes a **root** planned node (one with no blockers, so no blocker branch to stack on), the system SHALL resolve the new worktree's base branch in this order: (1) the orchestrator's explicit `base_branch` when one was set at bootstrap; (2) otherwise the orchestrator's coordinator role's bound-task branch; (3) otherwise the project default branch. The orchestrator SHALL persist an optional `base_branch`, supplied (optionally) when the orchestrator is created and defaulting to empty. This composes with the existing gater-materialization rule for nodes that DO have blockers — a blocker-having node's base SHALL continue to resolve from the most-recently-bound `done` blocker, unchanged. The default-to-coordinator-branch behavior SHALL be backward-compatible: a coordinator on the project default branch yields roots on the project default branch.

#### Scenario: Root node uses the explicit orchestrator base branch

- **WHEN** an orchestrator was created with an explicit `base_branch` and a root planned node materializes
- **THEN** the new worktree is based on that explicit branch

#### Scenario: Root node defaults to the coordinator branch

- **WHEN** an orchestrator has no explicit `base_branch` set and a root planned node materializes
- **THEN** the new worktree is based on the orchestrator's coordinator role's bound-task branch

#### Scenario: Falls back to the project default when no base resolves

- **WHEN** a root planned node materializes and neither an explicit base branch nor a coordinator branch is resolvable
- **THEN** the new worktree is based on the project default branch, as before this change

#### Scenario: Blocker-having node base resolution is unchanged

- **WHEN** a planned node with one or more blockers materializes
- **THEN** its base branch is resolved from the most-recently-bound blocker's branch, exactly as before this change

#### Scenario: Orchestrator persists an optional base branch

- **WHEN** an orchestrator is bootstrapped with no base branch supplied
- **THEN** its persisted base branch is empty and root nodes fall through to the coordinator-branch default

### Requirement: Planned node may be typed as a sub-coordinator

The system SHALL allow a planned node to carry an optional **kind** discriminator with values `worker` (the default) and `subcoord`. A `worker`-kind node, or a node with no kind specified, SHALL behave exactly as the existing planned node (it materializes into a leaf worker). A `subcoord`-kind node SHALL additionally carry a **goal** — the prompt delivered to the sub-coordinator on materialization — and SHALL NOT carry a child-orchestrator name or coordinator-role name; those are derived at materialize time (the parent supplies only the goal and does not author the child's plan). The `subcoord` discriminator and its goal SHALL be persisted on the planned role so the gater can select the materialize path without re-resolving intent. Creating a `subcoord` node SHALL be a coordinator-only operation, rejecting a worker or freelance caller, identical to the worker-node guard. Leaf worker SHALL remain the default so a planner only spawns a sub-coordinator by explicit choice.

#### Scenario: Sub-coordinator node kind is accepted and persisted

- **WHEN** a coordinator creates a planned node with kind `subcoord` and a goal prompt
- **THEN** the node is persisted with the `subcoord` discriminator and its goal so the gater can select the coordinator materialize path

#### Scenario: Goal is required on a sub-coordinator node

- **WHEN** a coordinator creates a `subcoord` node without a goal prompt
- **THEN** the operation is rejected with an error that a sub-coordinator node requires a goal

#### Scenario: Absent kind defaults to leaf worker

- **WHEN** a planned node is created with no kind specified
- **THEN** it is treated as a leaf worker, unchanged from the substrate, with no child orchestrator created on materialization

#### Scenario: Non-coordinator cannot author a sub-coordinator node

- **WHEN** a worker or freelance role attempts to create a `subcoord` node
- **THEN** the operation is rejected with an error that only coordinators may author the plan

### Requirement: Gater materializes a sub-coordinator node as a distinct coordinator agent

When all blockers of a `subcoord` planned node have reached role-status `done`, the system SHALL materialize that node as a **distinct coordinator agent** — one new argus task with its own worktree and agent session — and SHALL NOT bind it to any existing task. On that single new task the system SHALL create both: (1) a **worker binding in the parent orchestrator** against the **pre-created planned role**, so the node occupies its slot in the parent's DAG and its worker-role status reaching `done` continues to gate the parent's dependents exactly as a leaf worker would; and (2) a **new child orchestrator** — its name auto-derived from the node and de-collided, with a coordinator role defaulted to `coord` — **bound to that same new task**. The parent SHALL NOT name the child orchestrator/coordinator role nor author the child's plan; the sub-coordinator owns its decomposition (it makes its own plan to deliver the goal, collaborating with the user or asking its parent coordinator for guidance). Because the new task therefore holds a worker binding in the parent and a coordinator binding in the child, the sub-coordinator SHALL nest under the parent through the existing multi-binding bridge — appearing as a single node in the parent's DAG while owning a separate DAG in its child orchestrator — with no new nesting mechanism and no shared task with the parent coordinator. The delivered prompt SHALL orient the new agent as a coordinator (pointing at the worker-spawn and plan-authoring tools) AND include the standing check-in order to message its parent coordinator and poll `hera_inbox` for go/wait before real work, followed by the node's goal. If the session fails to start after the bindings are written, the system SHALL end the new task's bindings and remove the freshly minted child orchestrator and coordinator role while leaving the pre-created planned role intact as authored data. The node SHALL then be held, NOT re-materialized: because a planned node leaves the planned set the instant it acquires any binding (the binding now exists-but-ended), the gater SHALL NOT spawn a second agent for it — matching the worker never-double-spawn guard. Materialization SHALL be idempotent — a `subcoord` node that already holds a binding SHALL NOT be materialized again.

#### Scenario: Sub-coordinator node materializes as its own agent

- **WHEN** the last blocker of a `subcoord` node reaches role-status `done`
- **THEN** a new task with its own worktree and agent is created, and it is not bound to any existing task

#### Scenario: Materialization writes both the parent worker binding and the child coordinator binding

- **WHEN** a `subcoord` node materializes
- **THEN** the new task is bound as a worker against the pre-created planned role in the parent orchestrator AND as the coordinator of a newly created child orchestrator

#### Scenario: Sub-coordinator nests under the parent via the existing bridge

- **WHEN** a `subcoord` node has materialized
- **THEN** it appears as a single node in the parent's DAG and owns a separate DAG in its child orchestrator, nesting through the existing multi-binding bridge with no shared task with the parent coordinator

#### Scenario: Materialized sub-coordinator boots oriented with its goal

- **WHEN** the gater materializes a `subcoord` node
- **THEN** the delivered prompt orients the agent as a coordinator, includes the check-in/poll-inbox standing order, and includes the node's goal

#### Scenario: Child orchestrator name is derived, not parent-supplied

- **WHEN** a `subcoord` node materializes
- **THEN** the child orchestrator's name is auto-derived from the node (de-collided) and its coordinator role defaults to `coord`, with no name taken from the authoring parent

#### Scenario: Sub-coordinator owns its plan

- **WHEN** a sub-coordinator agent has materialized with its goal
- **THEN** it authors its own sub-plan to deliver the goal (the parent supplied only the goal, not a child DAG), and may collaborate with the user or ask its parent coordinator for guidance

#### Scenario: Worker-role status still gates the parent's dependents

- **WHEN** a `subcoord` node's worker role in the parent reaches role-status `done`
- **THEN** the parent's dependents blocked on that node become eligible to materialize, exactly as for a leaf-worker blocker

#### Scenario: Failed start leaves the planned role intact but held

- **WHEN** a `subcoord` node's bindings are written but the agent session fails to start
- **THEN** the new task's bindings are ended and the freshly minted child orchestrator and coordinator role are removed, while the pre-created planned role is left intact as authored data; the node is held (not re-materialized), because its binding now exists-but-ended and it has therefore left the planned set

#### Scenario: No agentless sub-coordinator

- **WHEN** a `subcoord` node materializes
- **THEN** the resulting sub-coordinator has its own distinct agent and does not share the parent coordinator's task

### Requirement: A planned node is excluded once its parent orchestrator archives or is nuked

The system SHALL treat a planned node as no longer eligible for gating or materialization once its parent orchestrator has been archived or nuked, even though the node's own `archived_at`/`cancelled_at` are unaffected by an orchestrator-level action. Archiving or nuking an orchestrator SHALL cascade-cancel (stamp `cancelled_at`) every still-planned (never-materialized, not already archived or cancelled) worker-kind child role belonging to it, at the moment of the archive/nuke. Independently of that cascade, the planned-node listing query SHALL also exclude any node whose parent orchestrator has `archived_at` or `nuked_at` set, regardless of the node's own `cancelled_at` — so a node that predates this fix (its orchestrator ended before the cascade existed) is excluded without requiring any data migration.

#### Scenario: Archiving an orchestrator cancels its still-planned children

- **WHEN** a coordinator's orchestrator with a never-materialized planned child node is archived
- **THEN** the child node's `cancelled_at` is stamped

#### Scenario: Nuking an orchestrator cancels its still-planned children

- **WHEN** an orchestrator with a never-materialized planned child node is nuked
- **THEN** the child node's `cancelled_at` is stamped

#### Scenario: A materialized (already-bound) child is not cancelled

- **WHEN** an orchestrator is archived or nuked
- **THEN** any child role that already holds a binding (materialized) is left untouched — cascade-cancel only reaches never-bound planned nodes

#### Scenario: A planned node under an archived orchestrator is excluded from the gate even without cascade-cancel having run

- **WHEN** a planned node's parent orchestrator has `archived_at` or `nuked_at` set, regardless of whether the node itself carries `cancelled_at`
- **THEN** the node does not appear in the set of nodes the gater evaluates for materialization

### Requirement: Materialization failures escalate after repeated retries

The system SHALL track, per planned node, the number of CONSECUTIVE materialization failures since the node last succeeded or was last evaluated as a fresh planned node. When that count reaches a bounded threshold, the system SHALL send a ONE-TIME escalation notice to the coordinator naming the node and the last error, instead of continuing to retry in total silence. The system SHALL NOT automatically cancel, reconfigure, or guess a fix for a node that has crossed the threshold — escalation is advisory only, and the node SHALL remain planned and continue to be retried on the normal tick schedule. A node that later succeeds, or that is no longer a planned node (materialized, cancelled, or removed), SHALL have its failure count and escalation state cleared.

#### Scenario: A node under the threshold retries silently

- **WHEN** a planned node's materialization fails fewer than the escalation threshold's consecutive times
- **THEN** no notice is sent and the node remains planned for the next tick

#### Scenario: Crossing the threshold sends a one-time notice

- **WHEN** a planned node's materialization fails the escalation threshold's consecutive times
- **THEN** the coordinator receives exactly one escalation notice naming the node and the last error

#### Scenario: The notice does not repeat every subsequent tick

- **WHEN** a node continues failing after already crossing the escalation threshold
- **THEN** no further escalation notice is sent for that node

#### Scenario: A later success clears the failure count

- **WHEN** a previously-failing planned node materializes successfully
- **THEN** its consecutive-failure count and escalation state are cleared

#### Scenario: Escalation never auto-cancels or reconfigures the node

- **WHEN** a planned node crosses the escalation threshold
- **THEN** the node's `argus_project`, prompt, and `cancelled_at` are left unchanged — only a notice is sent

### Requirement: Gater auto-accepts a materialized node's blockers

When the gater materializes a worker-kind planned node, it SHALL fire the same accept-equivalent `hera_accept` provides – marking `complete` and notifying – for EVERY one of that node's blockers, sourced from the SAME blocker-id list already resolved for the fan-in notice (no additional query, no new trigger point). This is the DAG's own version of "the coordinator rolling forward to the next item": the instant a dependent materializes, every one of its blockers has, by definition, just had its `done` signal consumed by the DAG moving past it.

This auto-accept SHALL be:

- **Best-effort** – an accept-equivalent failure for one blocker SHALL be logged and SHALL NOT fail the materialization that already succeeded, nor prevent the accept-equivalent from being attempted for the node's other blockers.
- **Idempotent** – a blocker gated by two or more dependent nodes has its accept-equivalent fired once per dependent that materializes, but only the FIRST such call produces a status flip and a notification; every subsequent call against the now-already-`complete` task is a silent no-op (the same idempotency `hera_accept` itself provides).
- **Scoped to the ordinary worker-kind materialize path** – a ROOT node (no blockers) fires no accept calls. A `subcoord` node's OWN materialization does not retroactively auto-accept its blockers through this mechanism (mirrors the existing fan-in notice's own worker-kind-only scope); a subcoord node's blockers ARE still accepted whenever some OTHER, ordinary dependent of theirs materializes through the normal path.

Derived from: `internal/heragater/heragater.go` (`acceptBlockers`, wired via `Accepter`/`SetAccepter`), `internal/hera/accept.go` (`AcceptRole`, the shared primitive also used by the `hera-coordination` capability's `hera_accept` tool), `internal/daemon/daemon.go` (wiring the gater's `Accepter` to `hera.AcceptRole`).

#### Scenario: Materializing a node accepts its single blocker

- **WHEN** a planned node with exactly one blocker materializes
- **THEN** that blocker's bound task flips to complete and it receives the same acceptance notification `hera_accept` sends

#### Scenario: Materializing a fan-in node accepts every blocker

- **WHEN** a planned node with two or more blockers materializes
- **THEN** every one of those blockers' bound tasks flips to complete and each receives an acceptance notification

#### Scenario: A blocker already accepted by a sibling dependent is a no-op

- **WHEN** a blocker gates two dependent nodes and the first dependent's materialization has already accepted it
- **THEN** the second dependent's materialization fires the same accept-equivalent call against that blocker with no error, no second status write, and no second notification

#### Scenario: An accept failure does not affect materialization or sibling blockers

- **WHEN** the accept-equivalent call for one blocker of a fan-in node fails
- **THEN** the failure is logged, the node's materialization (which already succeeded) is unaffected, and the accept-equivalent is still attempted for the node's other blockers

#### Scenario: A root node materializing fires no accept calls

- **WHEN** a planned node with no blockers materializes
- **THEN** no accept-equivalent call is made

#### Scenario: No coordinator to accept as does not panic

- **WHEN** a node materializes in an orchestrator with no coordinator role
- **THEN** the gater logs the condition and proceeds without panicking; no accept-equivalent call is made

