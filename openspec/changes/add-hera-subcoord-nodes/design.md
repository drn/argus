# Design: Hera sub-coordinator plan nodes

## Context

The plan-DAG substrate (`add-hera-plan-substrate`, shipped #774) lets a coordinator author **planned nodes** — bindingless `hera_roles` rows that the gater materializes into live workers when their blockers reach role-status `done`. Every planned node is hardcoded to kind `worker` (`db.CreateHeraPlannedRole` sets `Kind = HeraKindWorker`; the gater calls `agent.MaterializeHeraWorker`). So a plan can only ever fan out into leaf workers under a single coordinator.

To delegate a chunk of work to a *sub-team* today, a coordinator has no plan-level primitive. Its only move is to call `hera_new_orchestrator` on **its own task**, which rebinds the caller's existing task as the new orchestrator's coordinator (multi-binding). When the *root* coordinator does this, the resulting sub-coordinator has **no agent of its own** — it is the parent wearing two hats.

Observed live: `plan-view-dogfood`'s coordinator role (id 337) is bound to `improving-the-dag`'s own task (`1781812920610374000`). Selecting the sub-coordinator in the rail shows the parent's session, because they are literally the same task.

This violates a long-standing hera invariant — verified against the original `anutron/hera` plugin: **every coordinator has its own agent**. In the original design a sub-coordinator was always a previously-spawned worker (or a freelancer) that promoted *itself* via `hera_new_orchestrator`; the agent already existed and was distinct. The DAG workflow is the first context where the *root* moonlights, because the plan tooling gave it no way to say "this node is a sub-team with its own agent."

## Goals

- Let a planner declare a plan node as a **sub-coordinator** rather than a leaf worker.
- When such a node's blockers complete, materialize it as a **distinct coordinator agent** — its own argus task, worktree, and session — handed a **goal**.
- Restore the invariant: one claude instance maps to exactly one rail element (one agent). No agentless sub-coordinators.
- Reuse the existing spawn/nesting machinery wholesale; introduce no new nesting mechanism.

## Non-Goals

- **Pre-authored sub-plans.** The parent hands a *goal*, not a ready-made child DAG. The sub-coordinator runs its own brainstorm/plan. The parent MAY embed rich brainstorm context in the goal prompt so the sub-coordinator needs little human interaction, but it does not get a child `tasks.md`.
- **View work.** Grouping, completion-at-a-glance, single-entry-with-drill-down DAG rendering, and fan-out nested boxes are out of scope here — they belong to `add-hera-plan-view` (PR #777). "Option B" rail folders (a non-agent foldable rail entry) are explicitly rejected: a non-agent foldable re-enshrines the exact anomaly this change removes; grouping belongs in the DAG view, not the rail.
- **Cross-orchestrator blocking edges.** Unchanged from the substrate (v1 sequences sub-teams at the parent level).
- **Reconciling the substrate's worker-only wording.** Left intentionally for the central archival pass (coord-owned). This change adds sub-coordinator requirements that stand *alongside* the substrate's worker-only ones rather than modifying them.

## Decisions

### D1 — A planned node carries a `kind`: `worker` (default) or `subcoord`

`hera_plan_node` and each `hera_plan` node spec gain an optional kind discriminator (default `worker`). A `subcoord` node additionally carries the **goal** (its delivery prompt is the goal) and nothing else. The parent supplies ONLY the goal — it does NOT name the child orchestrator or coordinator role, and does NOT author the child's plan. The sub-coordinator owns its decomposition: the gater auto-derives the child-orchestrator name from the node (de-collided) purely to establish the binding, and defaults the coordinator role to `coord`; the materialized sub-coordinator then makes its own plan to deliver the goal, collaborating with the user or asking its parent coordinator for guidance as it plans. Leaf worker stays the default so trivial phases do not spawn middle management — sub-coord is an explicit planner choice.

The stored planned role itself stays kind `worker` (see D2) — the `subcoord` discriminator is recorded as node metadata on the role so the gater knows which materialize path to take. This keeps the parent's DAG model (worker roles + blocking edges) byte-identical.

### D2 — The sub-coord node is a WORKER role in the parent orchestrator; only materialization differs

This is the load-bearing decision. A `subcoord` plan node is still a **worker role in the parent orchestrator**, exactly like any other planned node. Blocking edges, the gater's gate-on-done logic, hold/ping, base-branch resolution, and short-id naming all operate on it unchanged — the parent's DAG does not know or care that this node will become a sub-team.

What differs is *only* the materialize step. Instead of `MaterializeHeraWorker` (bind the planned role + start a worker agent), a `subcoord` node materializes via a new path that, on **one new task**:

1. Creates the new task's **worker binding in the parent** against the pre-created planned role (so it occupies its DAG slot and its worker-role status `done` still gates the parent's dependents — identical to a leaf worker).
2. Creates a **new child orchestrator** (name auto-derived from the node, de-collided) and a **coordinator role** (defaulted to `coord`) bound to the **same new task**.

Because the new task now holds a worker binding in the parent AND a coordinator binding in the child, it nests under the parent through the **existing `SubtreeOrchIDs` multi-binding bridge** (the worker-bridge / BUG-004 path). No `parent_orch_id` column, no new nesting traversal — this is precisely the "sub-coordinator is one node in the parent DAG while owning a separate DAG in its child" shape the substrate's `Plan DAGs compose hierarchically` requirement already describes. The only change is that the gater now *creates* that shape at materialize time, rather than waiting for a spawned worker to promote itself.

### D3 — Reuse `SpawnHeraCoordinator` + `CreateAndStart` + `AfterPersist` LIFO cleanup

The materialize-as-coordinator path is `agent.CreateAndStart` with an `AfterPersist` hook that writes both bindings (parent-worker against the planned role, child-coordinator against a fresh child orchestrator + role) in one transaction, joining CreateAndStart's LIFO compensating stack. This mirrors `SpawnHeraCoordinator` (which already creates orchestrator + task + coordinator role + binding with full unwind) and `MaterializeHeraWorker` (which binds a pre-created role). The compensating cleanup on a later `runner.Start` failure ENDs the parent-worker binding and DELETEs the freshly-minted child orchestrator + coordinator role — the planned role itself survives (it is authored plan data, consistent with `MaterializeHeraWorker`'s "end binding, never delete the role" rule).

### D4 — The materialized sub-coordinator boots oriented as a coordinator-with-a-goal

The delivered prompt is the coordinator orientation (à la `HeraCoordinatorOrientation`) — naming the child orchestrator and pointing at `hera_spawn_worker` / `hera_plan_*` / `hera_status` / `hera_send` — **plus** the existing check-in/poll-inbox standing order (`HeraCheckInOrientation`): before doing real work, message the parent coordinator and poll `hera_inbox` for go/wait. The goal prompt (the node's stored prompt) follows. The intended usage, baked into the orientation, is that the sub-coordinator **owns the decomposition**: it runs its own brainstorm against the goal and authors its own sub-plan, collaborating with the user or asking its parent coordinator for guidance as it plans. The parent does not pre-author any of this — it handed a goal, not a child DAG.

## Risks / Trade-offs

- **Resource cost.** Each sub-coord node is a full claude instance + worktree. Mitigated by D1 (sub-coord is opt-in, leaf-worker is the default) — the planner only pays for middle management when it chooses to.
- **Two-binding atomicity.** Materialization now writes two bindings + a child orchestrator on one task. If any insert fails the whole task must unwind. Mitigated by D3 (single transaction in the `AfterPersist` hook + LIFO compensation), reusing the proven `SpawnHeraCoordinator`/`MaterializeHeraWorker` discipline.
- **Additive-spec staleness.** The substrate's "planned node is kind worker" wording is left unmodified (coord decision: central archival pass reconciles it). The new requirements stand alongside it. Tracked, accepted.

## Migration Plan

Additive schema only: a node-kind discriminator recorded on the planned role (the goal already exists as the planned role's delivery prompt). No child-orchestrator or coordinator-role name is stored — both are derived at materialize time (child-orch auto-derived from the node, coord role defaulted to `coord`). Existing planned nodes have no discriminator → treated as `worker`, byte-identical to today. The gater's worker path is unchanged; the coordinator path is reached only by a `subcoord`-typed node. No REST/SPA surface.

## Open Questions

- Where to store the node-kind discriminator: a nullable `kind` column on `hera_roles` planned rows vs a role-scoped meta sidecar. Leaning toward an explicit nullable column on the planned-role insert path since it is queried by the gater each tick (a sidecar would add a per-tick lookup). Resolved during Stage 1.

## Alternatives considered

- **Expose `SpawnHeraCoordinator` as an MCP tool a coordinator calls at runtime.** Lets an agent spawn a distinct sub-coordinator imperatively, but it is *not declarative* — the plan can't record the sub-team as a gated node, so sequencing and the durable-plan benefit are lost. Rejected as the primary mechanism (though the same primitive is reused internally by D3).
- **Make a planned node's stored role kind `coordinator`.** Would break the parent's DAG model (the gater, blocking edges, and `SubtreeOrchIDs`'s worker-bridge all key on the node being a worker role in the parent). The node must be a worker in the parent to occupy its DAG slot; coordinator-ness lives only in the child. Rejected.
- **Option B — rail folders (non-agent grouping nodes).** Solves visual jumble but re-introduces the agentless foldable this change removes, and pushes a view concern into the rail. Grouping belongs in `add-hera-plan-view`. Rejected.

## Acceptance criteria

### Authoring a sub-coordinator node (`hera_plan_node` / `hera_plan`)

- it should accept a node kind of `subcoord` (default `worker` when omitted)
- it should require a goal prompt on a `subcoord` node
- it should persist the `subcoord` discriminator on the planned role so the gater can select the materialize path
- it should reject a `subcoord` node authored by a non-coordinator caller (same guard as worker nodes)
- it should treat a node with no kind as a leaf worker, unchanged from the substrate

### Gater materializes a sub-coordinator node as a distinct coordinator agent

- it should, when a `subcoord` node's blockers all reach role-status `done`, create one new task with its own worktree and agent
- it should bind that new task as a worker against the pre-created planned role in the parent orchestrator
- it should create a new child orchestrator with a coordinator role bound to that same new task
- it should make the new sub-coordinator nest under the parent via the existing multi-binding bridge (it appears as one node in the parent and owns its own child orchestrator)
- it should deliver a coordinator orientation plus the check-in/poll-inbox standing order plus the goal prompt to the new agent
- it should leave the planned role intact (binding ended only) if the session fails to start, so the gater can retry
- it should not produce a sub-coordinator that shares the parent's task (no agentless sub-coordinator)

### Default behavior preserved

- it should materialize a `worker`-kind (or kind-absent) node exactly as the substrate does today, with no child orchestrator created
