## ADDED Requirements

### Requirement: Slow prelaunch does not time out task startup

The system SHALL allow a `StartSession` request to remain pending through the bounded backend prelaunch budget at every RPC hop. Other routine daemon RPCs SHALL retain their short timeout. A completed prelaunch failure SHALL be returned to the task creator before transactional cleanup, so the worktree is not removed while prelaunch is still running.

#### Scenario: Pi prelaunch takes longer than the ordinary RPC timeout

- **WHEN** Pi prelaunch takes more than two seconds and succeeds within its configured budget
- **THEN** the daemon and supervisor SHALL complete `StartSession` without an RPC timeout
- **AND** the task SHALL remain attached to its worktree

#### Scenario: Pi prelaunch fails

- **WHEN** Pi prelaunch returns an error within its configured budget
- **THEN** the error SHALL reach the task creator
- **AND** the creator SHALL unwind the task only after the start call has completed

## MODIFIED Requirements

### Requirement: Post-exit session-ID capture for capture-style backends

For OpenCode the system SHALL read the SQLite `session_v2` table used by v2 first, then the older SQLite `session` table, then legacy JSON session files. Each table SHALL be filtered by the task worktree's canonical directory and choose the most recently updated valid `ses_` identifier.

#### Scenario: OpenCode v2 session is captured

- **WHEN** a task's OpenCode v2 session exists in `session_v2`
- **THEN** its validated ID SHALL be captured for later resume
