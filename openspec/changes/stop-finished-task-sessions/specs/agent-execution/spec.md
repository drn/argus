## MODIFIED Requirements

### Requirement: Stop semantics

The runner SHALL stop a live session by signaling termination to the session's entire process group (the agent process and every descendant sharing its group, such as stdio MCP servers), escalating to a forced kill of that group after a short grace period, and SHALL mark the stop as explicit so the finish callback reports it. It SHALL never signal a process group that the runner's own process belongs to. Stopping a task with no live session SHALL return a not-found error. Stopping a session whose process has already exited SHALL be a no-op success. Stop-all SHALL terminate every live session and unblock any in-flight pre-launch work.

#### Scenario: Stop unknown task errors
- **WHEN** stop is requested for a task with no live session
- **THEN** a session-not-found error is returned

#### Scenario: Stop already-exited session is a no-op
- **WHEN** stop is requested for a session whose process has already exited
- **THEN** it returns success without error

#### Scenario: Stop reaches the agent's child processes
- **WHEN** a session whose agent has spawned child processes in its process group (e.g. a Playwright MCP server) is stopped
- **THEN** those children are terminated along with the agent, forcibly if they outlive the grace period

## ADDED Requirements

### Requirement: Finished tasks' sessions are stopped once idle

The daemon SHALL periodically stop the live agent session of any task whose status is complete or which is archived, regardless of which path made it so. A session SHALL be stopped only after it has been observed idle (no recent output, no restart pending), and its task observed finished, on two consecutive checks. Any check where it is not idle SHALL reset that wait, so an agent that marks its own task complete is never stopped mid-response.

A session first observed while its task was already finished SHALL NOT be stopped for that session's lifetime (a deliberate restart of a finished task), except on the daemon's first check after startup, which SHALL treat every finished task's idle session as eligible. Only sessions held by the argus runner SHALL ever be considered. Every stop SHALL be logged, and a failed stop SHALL be logged and retried on a later check.

#### Scenario: Completed task's idle session is stopped
- **WHEN** a running task is marked complete and its session then stays idle across two consecutive checks
- **THEN** the session is stopped

#### Scenario: Self-completing agent finishes its reply first
- **WHEN** an agent marks its own task complete and keeps producing output afterwards
- **THEN** its session is not stopped until it has gone idle across two consecutive checks

#### Scenario: Archived task's session is stopped
- **WHEN** a task with a running session is archived and the session stays idle across two consecutive checks
- **THEN** the session is stopped

#### Scenario: Unfinished tasks are untouched
- **WHEN** a task is pending, in progress or in review and not archived
- **THEN** its session is never stopped by this check

#### Scenario: Deliberate restart of a finished task is respected
- **WHEN** a session is started for a task that is already archived, after the daemon's first check
- **THEN** that session is not stopped by this check

#### Scenario: Startup clears the backlog
- **WHEN** the daemon starts with idle sessions whose tasks are already complete or archived
- **THEN** those sessions are stopped after two consecutive idle checks

#### Scenario: Pending restart is not stopped
- **WHEN** a finished task's session has a kick or recycle restart in flight
- **THEN** it is not considered idle and is not stopped
