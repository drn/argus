## ADDED Requirements

### Requirement: Finished-task dev-stack diagnostic

`argus doctor` SHALL additionally report any running dev-stack process whose worktree path matches a task row that is `complete` or archived, listing PID, process name, worktree path and task name. This is reported separately from the missing-worktree orphan list, regardless of whether the task has a live session. It SHALL be advisory-only (no effect on the exit code), read-only (no signaling), and SHALL report an unavailable scan or task lookup as unknown rather than none found.

#### Scenario: Stack of a complete task found

- **WHEN** a `mysqld` runs against the worktree of a `complete` task
- **THEN** `argus doctor` lists it as a finished-task stack with its task name

#### Scenario: Unfinished task not flagged

- **WHEN** a dev-stack process runs against the worktree of an `in_progress` task
- **THEN** it is not listed

#### Scenario: Unknown on scan or lookup failure

- **WHEN** the process scan or the task lookup fails
- **THEN** the section reports unknown, not none found

#### Scenario: Never mutates

- **WHEN** finished-task stacks are reported
- **THEN** no process is signaled and the exit code is unchanged
