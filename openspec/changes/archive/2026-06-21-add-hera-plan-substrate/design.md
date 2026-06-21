## Context

Argus once carried a declarative `depends_on` task-dependency DAG: tasks declared upstream dependencies, a `depswatcher` auto-started a blocked task once its deps completed, a halt cascade propagated on milestone failure, and the graph was laid out for inspection. It was retired in favor of **Hera** (coordinator-driven worker spawning) as the single orchestration model. That trade removed the *durable record of what blocks what* and pushed it into the coordinator LLM's context window.

A code survey of the current state (see Discovery findings) confirms: there is no plan concept left; the only spawn path is born-bound (a role is always created together with a live agent + worktree + inbox); `hera_role_status` already carries a `blocked` value but nothing gates on it; and the message bus is freeform text. The model is one decision away from supporting a durable plan: decouple a node from its agent.

This change adds the **substrate** only. Re-pointing the `dagview` widget at the new edges (short-id labels, collapsing parallel groups, navigation) is a deliberately separate follow-up change so the substrate can land and be tested headlessly.

## Goals / Non-Goals

**Goals:**

- A durable, declarative plan node that is **not** an agent: a hera role with no live binding, costing one DB row.
- Blocking edges between roles, cycle-safe, that the system can gate on.
- A gater that materializes a planned node into a live worker the moment its blockers are satisfied — mechanically, with no LLM in the spawn loop.
- Keep the coordinator's judgment in the loop via a **check-in** the materialized worker pulls, not a push it waits for.
- Agent-authored plans over MCP; no human form editor.

**Non-Goals:**

- The tree rendering (short-id layout, collapsing groups, navigation) — follow-up view change.
- Boundary-piercing cross-orchestrator edges (a node inside one sub-team directly blocking a node inside another) — v1 sequences sub-teams at the parent level instead.
- Resurrecting any retired surface: `/api/dag`, the standalone DAG tab, the SPA DAG view, `task_link`/`unlink`/`deps`/`halt_downstream`/`set_plan_slug`.
- A general task-dependency model on `model.Task` — the plan lives entirely in hera roles/edges, not back on `model.Task.DependsOn`.

## Decisions

- **A node is a planned hera role (no binding), not a `model.Task` row with deps.** Reuses the role/orchestrator model, the subtree machinery, and the messaging identity hera already owns. A planned node = a `hera_roles` row whose role has no live binding yet. Materialization = creating the binding + agent via the *existing* `agent.CreateAndStart`, targeting the pre-created role rather than minting a fresh one. Alternative (revive `model.Task.DependsOn`): rejected — it rebuilds a parallel subsystem instead of extending hera, and re-introduces the column we deliberately dropped.
- **Blocking edges are first-class rows, not messages.** `hera_blocks(blocked_role_id, blocker_role_id)`, FK to `hera_roles`, DFS cycle-check on insert. The bus stays freeform; ordering is structural data the gater can query, not text it must parse. v1 constrains both endpoints to the same orchestrator (simpler cycle-check, no cross-orchestrator bridge semantics).
- **Mechanical gate, dialogue steering.** The daemon gater spawns the instant state says unblocked (deterministic state machine). Adaptivity moves out of the gate and into a conversation: the materialized worker boots with a standing order to check in with its coordinator and **poll `hera_inbox`** for a go/wait. Alternative (coordinator-pull gating: notify the coord and let it spawn): rejected — it puts the LLM back in the spawn loop. Alternative (per-node "pause for review" flag): rejected — the pause becomes a static attribute predicted at authoring time rather than a judgment the coordinator makes in the moment.
- **Check-in is worker-pulled, never pushed.** A known hera gotcha: mid-flight messages to a busy/fresh worker are often missed because the idle-gated doorbell does not reliably wake it. So the bootstrap prompt instructs the worker to send its check-in and then **poll `hera_inbox` in a loop** until it reads go/wait. Durable reads are reliable; pushed doorbells are not. Building it as "wait to be told" would hang workers on a go that silently never arrived.
- **Gate on role-status `done` only; the coordinator is the backstop.** The gate is the blocker's hera **role status** reaching `done` (the worker's explicit "I'm finished"), NOT the argus **task status** — a finished hera worker rolls its task to `in_review` (never auto-`complete`), so gating on task status would fire prematurely (on `in_review`) or never (on `complete`). A blocker still `working` (e.g. iterating on CI by pushing PRs) does not open the gate, so the next phase never starts under churning work. A blocker whose session ends without ever reaching role-status `done` (crash/failure) **holds** the dependent and pings the coordinator — never spawn-and-park behind dead or unfinished work. The check-in remains the coordinator's veto for the "done, but let me verify/merge first" case.
- **Hierarchical composition: one DAG per orchestrator, sub-coords bridge.** Each orchestrator owns its own plan DAG. A sub-coordinator (a task that is a worker in its parent orchestrator and the coordinator of a child orchestrator) is a single node in the parent's DAG while owning a separate DAG in its child. Whole-phase sequencing across sub-teams is expressed at the parent level (an intra-parent edge between the sub-coords' worker roles); the parent's dependents gate on a sub-coord worker-role reaching `done`. Only boundary-*piercing* edges (a node inside one sub-team directly blocking a node inside another) are out of scope. When a fine dependency would otherwise need a piercing edge, the **planner reshapes the plan** so the dependency becomes a clean phase boundary — e.g. if only `1f` (not all of phase 1) blocks `2a`, the planner promotes `1f` into its own phase so the remaining `1*` and `2*` run concurrently and the precise gate is expressed phase-to-phase. **Seeing into a sub-coord from the parent** (its inner DAG) is the expand-a-collapsed-node interaction, handled in the follow-up view change — the parent's *dependency* model still treats the sub-coord as one node.
- **No concurrency cap.** When many nodes unblock at once they all materialize; throttling is the coordinator's job via the check-in (it tells the excess to "wait"). Keeps the gate mechanical and the steering dialogue, and avoids a tuning knob that would only ever be a guess.
- **Short-ids are an authoring-time naming discipline.** The planner assigns a stable short-id (`number` = stage, `letter` = parallel member) baked into the node name as a prefix (`2c-fact-checker`). Stable under edits; the *rendering* of short-ids is the follow-up view change.

## Risks / Trade-offs

- **A still-`working` blocker that pushed a PR must not be read as a satisfied gate** → the gater keys on hera **role status** `done`, never argus task status, so a CI-iterating phase never starts its dependents early.
- **A planned role with no binding may confuse existing binding-keyed code paths** (reconciliation, subtree roll-up, the rail) → audit every consumer that assumes a role has a live binding; the daemon-startup `ReconcileBindings` sweep is keyed on task-row existence and must not end or mangle bindingless planned roles.
- **The gater could double-spawn on a status flap** (a blocker toggling done→working→done) → materialization must be idempotent and guarded: a node already bound (or already `ready`/spawning) is never materialized twice.
- **Short-id assigned-number can drift from computed stage** after plan edits → accepted; the baked-in name is the stable handle, the row grouping (view concern) can float.
- **Stale/deleted blocker role** (a blocker role removed mid-plan) → mirror the retired DAG contract: unknown blocker ids are pruned, not fatal; a node whose blockers all vanished is treated as unblocked.
- **A cycle slipping in via concurrent edge inserts** → cycle-check inside the same transaction as the insert.

## Migration Plan

- Additive only: new `hera_blocks` table; no column drops, no data migration. Existing born-bound `hera_spawn_worker` is untouched.
- The gater is inert unless a plan exists (no planned nodes, no edges → nothing to materialize), so the change is dark for every current orchestrator until someone authors a plan.
- Rollback: drop the `hera_blocks` table and disable the gater; planned-but-unmaterialized roles are inert rows that can be deleted.

## Alternatives considered

- **Status quo (coordinator-driven only).** The coordinator spawns workers in order from memory. Flexible and adaptive, but the dependency structure is ephemeral and parallelism depends on the LLM remembering to fan out. This is the pain we are fixing.
- **Pure declarative gating (revive `depends_on` wholesale).** A static graph with a watcher that auto-starts everything. Durable and enforced, but rigid — and rigidity is *why* the first DAG was retired. Real plans change mid-flight.
- **Chosen: declarative skeleton + mechanical gate + dialogue check-in.** Durable structure and enforced ordering (from the declarative side) plus the coordinator's in-the-moment adaptivity (from the check-in). Best of both; the gate is dumb and the steering is a conversation.

## Discovery findings

From a read-only survey of the live schema and spawn path:

- **No plan concept remains.** `depends_on` / `plan_slug` were dropped with explicit `ALTER TABLE … DROP COLUMN` statements.
- **Born-bound is the only spawn path.** `CreateHeraRoleWithBinding` always stamps a live binding inside `agent.CreateAndStart`'s `AfterPersist`, and `runner.Start` fires immediately. There is no bindingless role path today — that single coupling is what this change un-couples.
- **`hera_role_status` already has a `blocked` value, but it is advisory** — nothing gates on it.
- **The message bus is freeform** (body + TLDR + delivery_mode); the daemon never inspects content — so ordering belongs in edge rows, not messages.
- **`base_branch` survives** as the git-stacking mechanic but is unused by the hera spawn path; it becomes the natural way to stack a dependent worker on its blocker at materialize time.
- **Prior art:** the retired `depswatcher` was a ~60s tick-loop that found pending tasks whose deps were all complete and called `StartPendingBlocked` — recoverable as a *pattern*, now gating on a planned role's blockers instead of a pending task.

## Acceptance criteria

**Planned node creation**

- it should create a hera role with no live binding (no agent, worktree, or inbox)
- it should reject a planned-node create from a non-coordinator caller

**Blocking edges + cycle check**

- it should reject a blocking edge that would introduce a cycle
- it should reject a blocking edge whose endpoints are in different orchestrators
- it should treat a node whose blocker role no longer exists as no longer blocked by it

**Gater materialization**

- it should materialize a planned node into a live worker once all its blockers reach done
- it should not materialize a node that is already bound or already spawning (idempotent)
- it should leave a node planned while any blocker is not yet done

**Check-in contract**

- it should boot a gater-materialized worker with a standing order to check in and poll hera_inbox before real work
- it should deliver the coordinator's go/wait reply via the worker's pulled inbox read, not a passive push

**Gate / hold**

- it should materialize a dependent only when every blocker's hera role status is done
- it should keep a dependent planned when a blocker is still working (e.g. iterating on CI)
- it should hold a dependent and ping the coordinator when a blocker's session ends without reaching role-status done

**Hierarchical composition**

- it should treat a sub-coordinator as one node in the parent DAG while it owns a separate DAG in its child orchestrator
- it should sequence sub-teams via a parent-level edge between their worker roles, gating on the sub-coordinator's worker-role reaching done
- it should keep sibling sub-teams' DAGs independent when no parent-level edge connects them

**Short-id convention**

- it should accept a planner-assigned short-id baked into the node name as a prefix and keep it stable across plan edits

## Open Questions

- **Boundary-piercing cross-orchestrator edges** — deferred; v1 sequences sub-teams at the parent level (see the hierarchical-composition decision). A direct edge from a node inside one sub-team to a node inside another is worth revisiting once there's a worked use case.
- **No concurrency cap (decided)** — a wide unblock materializes all ready nodes at once and the coordinator throttles via the check-in. Revisit only if unbounded fan-out proves painful in dogfooding.
