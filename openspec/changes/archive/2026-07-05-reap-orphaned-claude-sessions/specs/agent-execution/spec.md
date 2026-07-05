# Agent Execution

## ADDED Requirements

### Requirement: Orphaned Claude Code background-session reaping on stop

When the runner stops a task's session, it SHALL additionally check, in a background goroutine that does not delay the stop call's return, whether Claude Code itself is still hosting a live background session under that task's worktree directory — a session that was detached (backgrounded) to Claude Code's own per-user supervisor process and is therefore unreachable by the signal the runner just sent to its own tracked process. Any such session SHALL be stopped via Claude Code's own CLI.

Only sessions Claude Code reports as backgrounded, and currently alive, SHALL be stopped by this check; the task's own tracked interactive session SHALL never be targeted by it. The check SHALL identify a background session by the short id Claude Code's CLI assigns it, never by a full session UUID.

Every failure in this check — the CLI being unavailable, a listing failure, or a stop failure — SHALL be logged and SHALL NOT alter the runner's stop call's return value or error, and SHALL NOT block or delay it.

#### Scenario: No background session under the worktree

- **WHEN** a task's session is stopped and Claude Code reports no background session under that task's worktree
- **THEN** the stop call returns exactly as it did before this check existed, and nothing further happens

#### Scenario: A backgrounded, still-alive session is stopped

- **WHEN** a task's session is stopped and Claude Code reports a background session under that task's worktree that is still alive
- **THEN** the runner additionally stops that background session via Claude Code's CLI, using its short id, and logs the outcome

#### Scenario: The task's own tracked session is never targeted

- **WHEN** Claude Code reports the task's own interactive session under that task's worktree
- **THEN** the check does not attempt to stop it, since it is not reported as backgrounded

#### Scenario: An already-exited background entry is skipped

- **WHEN** Claude Code reports a background session under the task's worktree that is no longer alive
- **THEN** the check does not attempt to stop it

#### Scenario: The check never delays or fails the stop call

- **WHEN** the Claude Code CLI is unavailable, or listing or stopping a background session fails
- **THEN** the failure is logged and the runner's stop call's return value and timing are unaffected
