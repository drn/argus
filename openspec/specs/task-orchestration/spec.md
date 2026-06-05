# Task Orchestration (DAG)

## Purpose

Argus lets an orchestrator agent build a directed acyclic graph of tasks where each task can declare upstream dependencies (`depends_on`). This capability owns the **dependency-graph engine and its views** — the layer beneath any transport. It resolves that graph: it auto-starts blocked tasks once their dependencies complete, cascades a halt across downstream tasks when a milestone fails, and lays out the graph visually so users can inspect a stack at a glance. It also owns the underlying **graph primitives** — the link/unlink mutations, cycle detection (including the cycle-path ordering it reports), and one-hop neighbour computation — expressed here as graph semantics and engine invariants, independent of how any caller is wired in. Dependency status — not the agent-supplied result payload — is the only gate the daemon enforces; interpreting failure is the orchestrator's job.

The REST/HTTP surface that drives these primitives (status codes, request/response shapes, the cycle→409 mapping, device-token reach) is owned by `task-linking`; this spec deliberately states engine behaviour without restating HTTP status codes.

## Requirements

### Requirement: Auto-start of blocked tasks when dependencies complete

The dependency watcher SHALL periodically scan all tasks and start any task that is pending, unarchived, and has a non-empty `depends_on` list whose every referenced dependency has reached the complete status. Tasks with no dependencies SHALL NOT be started by the watcher. The watcher SHALL gate solely on dependency status and SHALL NOT inspect the dependency's result payload.

#### Scenario: All dependencies complete

- **WHEN** a pending unarchived task's only dependency has reached the complete status
- **THEN** the watcher starts the task's agent session and transitions the task to in-progress

#### Scenario: A dependency is not yet complete

- **WHEN** a pending task depends on two tasks and one of them is still in progress
- **THEN** the watcher leaves the task pending and starts no session

#### Scenario: Task has no dependencies

- **WHEN** a pending task has an empty `depends_on` list
- **THEN** the watcher takes no action on that task

#### Scenario: A failed dependency still unblocks downstream

- **WHEN** a dependency reached the complete status carrying a result payload indicating failure (e.g. `{"failed":true}`)
- **THEN** the watcher still treats the dependency as resolved and starts the dependent task, leaving the failure interpretation to the orchestrator

### Requirement: Blocked tasks with broken or archived dependencies remain blocked

The watcher SHALL treat a dependency referenced by an ID that is absent from the database as unresolved and SHALL refuse to start the dependent task. The watcher SHALL never start an archived task even when its dependencies are complete.

#### Scenario: Dependency ID missing from the database

- **WHEN** a pending task depends on an ID that no longer exists in the database
- **THEN** the watcher leaves the task pending and starts no session

#### Scenario: Archived blocked task

- **WHEN** a pending but archived task's dependency is complete
- **THEN** the watcher does not start the task

### Requirement: Race-safe and self-healing start

The watcher SHALL re-read each candidate task immediately before starting it and SHALL skip starting if the task is no longer pending or has become archived in the interim. A transient start failure SHALL leave the task pending so a subsequent scan can retry. The watcher SHALL run a scan immediately on startup so tasks unblocked while the daemon was down are caught up without waiting a full interval.

#### Scenario: Task advanced between scan and start

- **WHEN** a task observed as pending during the scan has been flipped to in-progress before the watcher re-reads it
- **THEN** the watcher skips it and starts no second session, leaving the newer status intact

#### Scenario: Task archived between scan and start

- **WHEN** a task observed as pending during the scan has been archived before the watcher re-reads it
- **THEN** the watcher skips it and starts no session

#### Scenario: Transient start failure then retry

- **WHEN** a start attempt fails and a later scan finds the task still pending with dependencies complete
- **THEN** the task remains pending after the failure and is started successfully on the retry

#### Scenario: Existing session present from a prior partial start

- **WHEN** a task is pending but a live agent session already exists for it (a prior start succeeded but the status write did not)
- **THEN** the watcher synchronises the task to in-progress without spawning a second process

### Requirement: Cycle detection on dependency links

The system SHALL reject any dependency link that would close a cycle, leaving the dependency state unchanged, and SHALL report the offending node sequence in dependency order (first and last element being the node that closes the cycle) so the cause is actionable. A self-link (a task depending on itself) SHALL be rejected as a cycle. Adding a dependency that already exists SHALL be a no-op.

#### Scenario: Link closes a cycle

- **WHEN** linking would create a cycle among existing dependencies
- **THEN** the operation is rejected with a cycle error whose path lists the nodes in the cycle, and no dependency is added

#### Scenario: Self-link

- **WHEN** a task is linked to depend on itself
- **THEN** the operation is rejected as a cycle

#### Scenario: Duplicate link

- **WHEN** a dependency that already exists is added again
- **THEN** the dependency list is unchanged and no error is returned

#### Scenario: Valid new link

- **WHEN** a non-cyclic dependency from child to parent is added
- **THEN** the parent ID is appended to the child's dependency list

### Requirement: Unlinking dependencies

The system SHALL remove a parent from a child's dependency list when requested, and SHALL treat removal of a non-existent dependency as a no-op. Empty task IDs SHALL be rejected.

#### Scenario: Remove an existing dependency

- **WHEN** an existing parent dependency is removed from a child
- **THEN** the child's dependency list no longer contains that parent

#### Scenario: Remove a dependency that is not present

- **WHEN** a removal targets a parent the child does not depend on
- **THEN** the dependency list is unchanged and no error is returned

### Requirement: One-hop dependency neighbours

The system SHALL report, for a given task, its direct upstream dependencies and the set of tasks that directly depend on it.

#### Scenario: Task with downstream dependents

- **WHEN** querying neighbours of a task that two other tasks depend on and that itself depends on nothing
- **THEN** the result lists no upstream entries and two downstream entries

#### Scenario: Task with an upstream dependency

- **WHEN** querying neighbours of a task that depends on one parent and has no dependents
- **THEN** the result lists that parent upstream and no downstream entries

### Requirement: Halt-downstream cascade

The system SHALL cascade a halt to all transitive descendants of a seed task without halting the seed itself. For each descendant the action SHALL depend on its current, freshly re-read status: in-progress descendants SHALL have their running session stopped; pending and in-review descendants SHALL be archived rather than stopped; complete descendants SHALL be left untouched. A stop that reports the session already exited SHALL NOT be counted as a stopped task. The operation SHALL return a report of which descendants were stopped, archived, or could not be found.

#### Scenario: Mixed-status descendants

- **WHEN** halting downstream from a seed whose descendants include a pending task, an in-progress task, and a complete task
- **THEN** the pending task is archived, the in-progress task's session is stopped, the complete task is left untouched, and the seed itself is neither stopped nor archived

#### Scenario: In-review descendant has no live session

- **WHEN** a descendant is in review (its agent already exited)
- **THEN** it is archived rather than stopped, and it does not appear in the stopped report

#### Scenario: Session already exited before stop

- **WHEN** stopping an in-progress descendant returns a session-not-found result
- **THEN** that descendant is not counted in the stopped report

### Requirement: DAG snapshot and plan-slug grouping

The system SHALL produce a filterable snapshot of DAG nodes, each projecting the task's id, name, status, archived flag, plan slug, result, and dependency list, with edges implied by the dependency list. Filters SHALL scope by project and plan slug, and archived nodes SHALL be excluded unless archived inclusion is explicitly requested. The plan slug SHALL be a freely-settable opaque grouping label that the daemon does not interpret.

#### Scenario: Archived nodes excluded by default

- **WHEN** listing the DAG without requesting archived inclusion
- **THEN** archived tasks are omitted from the snapshot

#### Scenario: Archived nodes included on request

- **WHEN** listing the DAG with archived inclusion enabled
- **THEN** archived tasks appear in the snapshot

#### Scenario: Project filter

- **WHEN** listing the DAG scoped to a single project
- **THEN** only nodes belonging to that project are returned

#### Scenario: Set and clear a plan slug

- **WHEN** a plan slug is set on a task and later cleared
- **THEN** the task carries the slug after the set and an empty slug after the clear

### Requirement: Deterministic layered DAG layout

The system SHALL lay out the DAG with each node assigned a layer equal to the longest dependency path from a source (sources at layer 0), order nodes within a layer to reduce edge crossings, and produce a deterministic ordering for identical input. Dependency references to nodes not present in the snapshot SHALL be silently dropped from the edge list rather than causing a failure, and a defective cyclic input SHALL degrade to a bounded layout rather than looping forever.

#### Scenario: Layer assignment by longest path

- **WHEN** laying out a chain or diamond of dependencies
- **THEN** each node is placed one layer deeper than its deepest parent, with parentless nodes at layer 0

#### Scenario: Stale dependency reference

- **WHEN** a node depends on an ID absent from the laid-out set
- **THEN** the layout renders the partial graph and omits the dangling edge

#### Scenario: Identical input

- **WHEN** the same node set is laid out twice
- **THEN** both layouts produce identical node ordering

### Requirement: DAG view node filtering

The DAG view SHALL exclude archived tasks and pure-orphan tasks — those with no live (non-archived, present) parent and not referenced as a parent by any surviving task — while retaining any task that participates in the linked graph, including a task whose only listed parents are stale but which still has a live child.

#### Scenario: Pure orphan dropped

- **WHEN** projecting a task set containing a linked parent-child pair and an unconnected standalone task
- **THEN** the standalone task is dropped and the linked pair is retained

#### Scenario: Archived task dropped

- **WHEN** the task set contains an archived task
- **THEN** the archived task is excluded from the projection

#### Scenario: Stale parent reference but referenced by a live child

- **WHEN** a task lists only a deleted parent ID yet a live child depends on it
- **THEN** the task is retained as a source node

#### Scenario: Node fields preserved through the projection

- **WHEN** a task with a name, status, result payload, and dependency list is projected
- **THEN** those fields are carried through and the dependency list is an independent copy
