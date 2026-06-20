## ADDED Requirements

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
