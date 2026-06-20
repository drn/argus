# Proposal: Hera sub-coordinator plan nodes

## Why

The plan-DAG substrate only ever fans out into leaf workers — every planned node is hardcoded kind `worker`. To delegate a chunk of work to a sub-team, a coordinator has to call `hera_new_orchestrator` on its own task, which (when the root does it) produces an agentless sub-coordinator (the parent wearing two hats), violating hera's long-standing "every coordinator has its own agent" invariant. A plan needs a way to *declare* a node as a sub-coordinator that materializes as a distinct agent given a goal.

## What Changes

- **Planned node kind.** A planned node gains an optional **kind** (`worker` default | `subcoord`). A `subcoord` node carries **only a goal** prompt — the parent does not name the child orchestrator/coordinator role or author the child's plan. The sub-coordinator owns its decomposition (makes its own plan to deliver the goal, collaborating with the user or asking the coordinator for guidance); the gater auto-derives the child-orchestrator name and defaults the coord role to `coord` purely to establish the binding. Leaf worker stays the default — sub-coord is an explicit planner choice (no middle management for trivial phases).
- **Coordinator materialize path.** When a `subcoord` node's blockers all reach role-status `done`, the gater materializes it as a **distinct coordinator agent** — one new task + worktree + agent — by writing a **worker binding in the parent** (against the pre-created planned role, so DAG gating is unchanged) **and** a **new child orchestrator with a coordinator role bound to the same new task**. It nests under the parent via the existing `SubtreeOrchIDs` multi-binding bridge; no new nesting mechanism.
- **Coordinator orientation + check-in.** The new agent boots with a coordinator orientation (it will brainstorm/plan its own sub-team against the goal) plus the existing check-in/poll-inbox standing order before real work.
- **Authoring tools.** `hera_plan_node` and `hera_plan` accept the `subcoord` kind + goal (coordinator-only, same guard as worker nodes).
- **Invariant restored.** One claude instance = one rail element (one agent). No agentless sub-coordinators.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `task-orchestration`: gains the sub-coordinator-node-kind requirement and the coordinator-materialize-path requirement (added alongside the existing planned-node/gater requirements). The existing `Plan DAGs compose hierarchically through sub-coordinators` requirement already anticipates this shape; this change makes the gater *create* it at materialize time.
- `hera-coordination`: the plan-authoring tool surface (`hera_plan_node`, `hera_plan`) gains the `subcoord` node kind + goal parameters.

## Impact

- **Code:** `internal/db/hera_plan.go` (node-kind discriminator on the planned-role insert path; `ListHeraPlannedNodes` surfaces the kind); `internal/heragater/heragater.go` (route `subcoord` nodes to the coordinator materialize path); `internal/agent/hera_spawn.go` (a `MaterializeHeraSubCoordinator` primitive reusing `CreateAndStart` + `AfterPersist` LIFO cleanup, mirroring `SpawnHeraCoordinator`/`MaterializeHeraWorker`); `internal/mcp/hera_plan.go` (accept + validate the `subcoord` kind + goal in `hera_plan_node`/`hera_plan`).
- **Schema:** additive only — a nullable node-kind on planned roles (the goal is the existing delivery prompt; no child-orch/coord-role names stored). Existing planned rows default to `worker` (byte-identical behavior).
- **No REST/SPA surface.** Hera-native only.
- **View deferred.** Grouping / drill-down / fan-out rendering is `add-hera-plan-view` (PR #777), not this change.

## Note on spec baseline (additive-only)

The `add-hera-plan-substrate` change is Complete but **not yet archived**, so the `task-orchestration` base spec is empty and its planned-node requirements (including the "kind `worker`" wording) live only in the substrate's delta. Per coordinator decision, this change does **not** archive the substrate and does **not** write MODIFIED deltas against it. The new sub-coordinator requirements are authored as **ADDED** requirements that stand alongside the substrate's worker-only wording. Reconciling that wording (broadening "planned node is kind worker" to admit the `subcoord` kind) is intentionally left to the central archival pass that will archive the in-flight plan changes (`add-hera-plan-substrate`, `add-hera-plan-view`, `add-hera-plan-base-branch`) together, in dependency order. The slight staleness is tracked and accepted; the additive framing is non-contradictory (sub-coord is purely additive to leaf-worker).
