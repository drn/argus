# Worktree Management

## MODIFIED Requirements

### Requirement: Pruning completed tasks

The system SHALL prune completed tasks in two phases: a synchronous phase
that removes completed rows from the database, stops their sessions, removes
their session logs, and computes the remaining worktree and orphan cleanup
counts; and a slow phase that removes each task's worktree and branch in
parallel and runs the orphan sweep. The slow phase SHALL execute at most once
per plan; subsequent invocations SHALL be no-ops. A completed task that still
holds a live Hera role binding (a `hera_bindings` row with `ended_at IS NULL`)
SHALL be excluded from both phases — `hera_bindings` holds no foreign key to
`tasks`, so deleting such a task's row would leave its Hera role pointing at a
task that no longer exists instead of properly ending it. The system SHALL
report the number of completed tasks skipped for this reason.

#### Scenario: Completed tasks pruned, active tasks retained

- **WHEN** a prune runs with one completed task (with a worktree on disk), one active task, and one orphan directory under the worktree root
- **THEN** only the completed task is removed from the database, its worktree and the orphan directory are both removed, and the active task and its row remain

#### Scenario: Nothing to prune

- **WHEN** a prune runs with no completed tasks
- **THEN** the plan reports zero pruned tasks and zero worktree cleanup work and performs no removal

#### Scenario: Slow phase runs at most once

- **WHEN** the slow prune phase is invoked a second time on the same plan
- **THEN** the second invocation is a no-op and fires no progress callbacks

#### Scenario: Completed task with a live Hera binding is skipped

- **WHEN** a prune runs with a completed task that still has a live (`ended_at IS NULL`) Hera role binding
- **THEN** that task's row, worktree, and branch are NOT removed, and it is counted separately as skipped rather than pruned

#### Scenario: Completed task with only an ended Hera binding is pruned normally

- **WHEN** a prune runs with a completed task whose Hera binding(s) all have `ended_at` set
- **THEN** the task is pruned exactly as a never-bound completed task would be
