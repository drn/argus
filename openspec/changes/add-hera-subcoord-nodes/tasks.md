**Design doc:** `openspec/changes/add-hera-subcoord-nodes/design.md`

## 1. Tests (failing first — Prove-It)

- [x] 1.1 `internal/db`: failing tests for the planned-node kind discriminator — `CreateHeraPlannedRole`/`CreateHeraPlan` persist `subcoord` kind + goal (goal = the existing delivery prompt; NO child-orch/coord-role names stored); `ListHeraPlannedNodes` surfaces kind + goal; absent kind defaults to `worker` (byte-identical to today)
- [x] 1.2 `internal/mcp`: failing tests for `hera_plan_node`/`hera_plan` accepting `kind=subcoord` + goal, rejecting a `subcoord` node with no goal, rejecting a non-coordinator caller, and mixing node kinds in a whole-graph `hera_plan`
- [x] 1.3 `internal/agent`: failing tests for `MaterializeHeraSubCoordinator` — one new task, parent worker binding against the pre-created role + new child orchestrator with a coordinator role bound to the SAME task; LIFO unwind on `runner.Start` failure ends bindings + deletes child orch/coord role but leaves the planned role; the new task is never the parent coordinator's task
- [x] 1.4 `internal/heragater`: failing tests for routing — a ready `subcoord` node calls the coordinator materialize path (not `MaterializeHeraWorker`); a `worker`/absent-kind node still calls the worker path; idempotent (already-bound `subcoord` node not re-materialized); worker-role `done` on a materialized sub-coord still gates the parent's dependents
- [x] 1.5 Confirm every `it should X` criterion in `design.md` maps to a failing test (Prove-It Pattern)

## 2. Authoring: store + MCP accept the sub-coordinator node kind

**Depends on:** Stage 1

- [ ] 2.1 `internal/db/hera_plan.go`: add the node-kind discriminator to the planned-role insert path — nullable `kind` column on the planned role (the goal is the existing delivery prompt; NO child-orch/coord-role names stored — resolves the design Open Question; queried by the gater each tick). `CreateHeraPlannedRole` and `CreateHeraPlan` accept kind defaulting to `worker`; keep existing worker rows byte-identical
- [ ] 2.2 `internal/db/hera_plan.go`: `ListHeraPlannedNodes` surfaces the kind + goal so the gater can branch without re-resolving intent
- [ ] 2.3 `internal/mcp/hera_plan.go`: `toolHeraPlanNode` + `toolHeraPlan` accept `kind` (default `worker`) + `goal` only (NO child-orch/coord-role-name params — parent hands just the goal); validate that a `subcoord` node has a goal; keep the coordinator-only guard; surface kind in the tool result text
- [ ] 2.4 uxlog/slog: log sub-coord node authoring (`[hera] plan_node kind=subcoord ...`) consistent with existing plan-authoring logs

## 3. Materialization: distinct coordinator agent + gater routing

**Depends on:** Stage 2

- [ ] 3.1 `internal/agent/hera_spawn.go`: add `MaterializeHeraSubCoordinator` — `CreateAndStart` + `AfterPersist` hook writing BOTH bindings (parent worker against the pre-created planned role + new child orchestrator — name auto-derived from the node and de-collided via `UniqueHeraOrchestratorName`, coord role defaulted to `coord` — with coordinator role bound to the same new task) in one tx, joining the LIFO compensating stack. Cleanup on later `runner.Start` failure ends the new task's bindings + deletes the child orch/coord role, leaving the planned role intact (mirror `MaterializeHeraWorker`'s role-survives rule + `SpawnHeraCoordinator`'s orchestrator-unwind)
- [ ] 3.2 `internal/agent/hera_spawn.go`: build the materialized sub-coord prompt — coordinator orientation (`HeraCoordinatorOrientation`-style, naming the child orch + spawn/plan/status/send tools) + the existing `HeraCheckInOrientation` standing order + the node's goal
- [ ] 3.3 `internal/heragater/heragater.go`: in `materializeNode`, branch on the planned node's kind — `subcoord` → the coordinator materialize path; `worker`/absent → unchanged `MaterializeHeraWorker`. Extend the `Materializer` seam / daemon adapter wiring as needed; preserve idempotency (no binding ⇒ planned) and base-branch resolution
- [ ] 3.4 uxlog/slog: log the coordinator-materialize path (`[heragater] materialized SUBCOORD node N (child_orch=...)`) including the new child orchestrator name; log failed-start unwind

## 4. Documentation

**Depends on:** Stage 3

- [ ] 4.1 `context/knowledge/gotchas/orchestration.md`: add sub-coord-node gotchas — the node stays a WORKER role in the parent (DAG model unchanged); materialize writes two bindings on one task; nesting is the existing `SubtreeOrchIDs` bridge (no new mechanism); LIFO unwind deletes the child orch but leaves the planned role; sub-coord is opt-in (worker default)
- [ ] 4.2 README Reference: update the MCP `hera_plan_node`/`hera_plan` parameter rows to note the `kind`/`goal` params (Reference appendix only — no top-half marketing edit)
- [ ] 4.3 Run `make pre-pr` and confirm green before any push
