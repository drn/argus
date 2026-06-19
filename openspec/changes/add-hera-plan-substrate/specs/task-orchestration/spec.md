## ADDED Requirements

### Requirement: Planned node is a hera role with no live binding

The system SHALL allow a coordinator to create a **planned node**: a `hera_roles` row of kind `worker` that has **no live binding** and therefore no agent process, no worktree, and no inbox. A planned node SHALL persist its target argus project, the prompt to deliver on materialization, and a planner-assigned short-id-prefixed name. Creating a planned node SHALL be a coordinator-only operation; a worker or freelance caller SHALL be rejected. A planned node SHALL NOT trigger `agent.CreateAndStart` or any worktree creation at plan time.

#### Scenario: Coordinator creates a planned node

- **WHEN** a coordinator creates a planned node with a name, prompt, and project
- **THEN** a worker-kind role is persisted with no live binding, and no agent, worktree, or inbox is created

#### Scenario: Non-coordinator cannot plan

- **WHEN** a worker or freelance role attempts to create a planned node
- **THEN** the operation is rejected with an error that only coordinators may author the plan

#### Scenario: Planned node carries its materialization inputs

- **WHEN** a planned node is created
- **THEN** its target project, delivery prompt, and short-id-prefixed name are persisted on the role for use at materialization

### Requirement: Blocking edges between roles with cycle detection

The system SHALL store directed blocking edges in a `hera_blocks(blocked_role_id, blocker_role_id)` table with foreign keys to `hera_roles`. On inserting an edge the system SHALL run a depth-first cycle check within the same transaction and SHALL reject any edge that would create a cycle. Both endpoints of an edge SHALL belong to the same orchestrator; an edge whose endpoints are in different orchestrators SHALL be rejected (single-orchestrator scope for this version). When a blocker role no longer exists, the system SHALL treat the dependent as no longer blocked by that missing edge rather than failing.

#### Scenario: Cycle is rejected

- **WHEN** adding a blocking edge that would make the graph cyclic
- **THEN** the insert is rejected and the edge is not stored

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

The system SHALL run a hera-native gater that watches role status and, when **all** of a planned node's blockers have reached `done`, materializes that node into a live worker by creating its binding and agent via the existing `agent.CreateAndStart` against the **pre-created** role (not a freshly minted role). Materialization SHALL be idempotent: a node that is already bound, already materializing, or already `ready`-and-claimed SHALL NOT be materialized again. A node with any blocker not yet `done` SHALL remain planned. At materialization the worktree and branch SHALL be created, with `base_branch` resolved from the now-existing blocker branches; when a node has multiple blockers, `base_branch` SHALL be the branch of the most-recently-bound blocker (the stack tip).

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

The gate SHALL be the blocker's hera **role status** reaching `done` — the worker's explicit declaration that its work is finished. This is distinct from the argus **task status**: a finished hera worker rolls its task to `in_review` (never auto-`complete`), so task status is NOT the gate; only role-status `done` is. A blocker still `working` — for example iterating on CI by pushing PRs — SHALL NOT satisfy the gate, so the next phase does not start under churning work. When a blocker's session ends without its role ever reaching `done` (a crash or other failure), the system SHALL **hold** the dependent (not materialize it) and notify the coordinator, so no worker is spawned and parked behind dead or unfinished work. The hold-notification SHALL be sent once per held (dependent, blocker) pair rather than repeated on each evaluation, so the coordinator is alerted without being spammed.

#### Scenario: Only role-status done opens the gate

- **WHEN** every blocker of a node has hera role status `done`
- **THEN** the node materializes

#### Scenario: A still-working blocker keeps the dependent planned

- **WHEN** a blocker is still `working` (e.g. iterating on CI) and has not reported role-status `done`
- **THEN** the dependent stays planned and does not start

#### Scenario: A crashed or unfinished blocker holds and notifies

- **WHEN** a blocker's session ends without its role reaching `done`
- **THEN** the dependent is held (not materialized) and the coordinator is notified

### Requirement: Planner-assigned stable short-id naming

The system SHALL accept a planner-assigned short-id prefix on a node's name, by convention `<stage-number><parallel-letter>` (e.g. `2c-fact-checker`), where the number orders serial stages and the letter enumerates parallel members. The short-id SHALL be stable across plan edits (it is a durable handle, not recomputed). Rendering the short-id in the orchestration tree is out of scope for this version.

#### Scenario: Short-id prefix is accepted and persisted

- **WHEN** a planned node is created with a `<number><letter>-<slug>` name
- **THEN** the short-id-prefixed name is persisted as the node's stable handle

#### Scenario: Short-id is not recomputed on edit

- **WHEN** the plan is edited such that a node's effective stage would change
- **THEN** the node's assigned short-id remains unchanged
