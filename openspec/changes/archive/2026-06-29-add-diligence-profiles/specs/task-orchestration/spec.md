# Task Orchestration

## MODIFIED Requirements

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
