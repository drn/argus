## ADDED Requirements

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

When all blockers of a `subcoord` planned node have reached role-status `done`, the system SHALL materialize that node as a **distinct coordinator agent** — one new argus task with its own worktree and agent session — and SHALL NOT bind it to any existing task. On that single new task the system SHALL create both: (1) a **worker binding in the parent orchestrator** against the **pre-created planned role**, so the node occupies its slot in the parent's DAG and its worker-role status reaching `done` continues to gate the parent's dependents exactly as a leaf worker would; and (2) a **new child orchestrator** — its name auto-derived from the node and de-collided, with a coordinator role defaulted to `coord` — **bound to that same new task**. The parent SHALL NOT name the child orchestrator/coordinator role nor author the child's plan; the sub-coordinator owns its decomposition (it makes its own plan to deliver the goal, collaborating with the user or asking its parent coordinator for guidance). Because the new task therefore holds a worker binding in the parent and a coordinator binding in the child, the sub-coordinator SHALL nest under the parent through the existing multi-binding bridge — appearing as a single node in the parent's DAG while owning a separate DAG in its child orchestrator — with no new nesting mechanism and no shared task with the parent coordinator. The delivered prompt SHALL orient the new agent as a coordinator (pointing at the worker-spawn and plan-authoring tools) AND include the standing check-in order to message its parent coordinator and poll `hera_inbox` for go/wait before real work, followed by the node's goal. If the session fails to start after the bindings are written, the system SHALL end the new task's bindings and remove the freshly minted child orchestrator and coordinator role while leaving the pre-created planned role intact, so the gater can retry on a later tick. Materialization SHALL be idempotent — a `subcoord` node that already holds a binding SHALL NOT be materialized again.

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

#### Scenario: Failed start leaves the planned role for retry

- **WHEN** a `subcoord` node's bindings are written but the agent session fails to start
- **THEN** the new task's bindings are ended and the freshly minted child orchestrator and coordinator role are removed, while the pre-created planned role is left intact for a later retry

#### Scenario: No agentless sub-coordinator

- **WHEN** a `subcoord` node materializes
- **THEN** the resulting sub-coordinator has its own distinct agent and does not share the parent coordinator's task
