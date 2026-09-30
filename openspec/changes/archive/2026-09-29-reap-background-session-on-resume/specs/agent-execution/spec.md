# Agent Execution

## ADDED Requirements

### Requirement: Background-session reaping before Claude resume

When the runner resumes a task on a Claude Code backend, it SHALL, before launching, stop any live Claude Code background session that is hosting that task's own conversation (matching the task's session id, listed under the task's worktree directory), so that Claude Code's own "session is running in the background" guard does not refuse the resume. The check SHALL complete before the launch begins, SHALL NOT target background sessions hosting other conversations, and SHALL NOT run for fresh (non-resume) starts, non-Claude backends, or tasks with no session id or worktree.

Every failure in this check SHALL be logged and SHALL NOT prevent the resume from being attempted.

#### Scenario: Resume with the conversation held by a background session

- **WHEN** a Claude task with a session id is resumed and Claude Code reports a live background session with that session id under its worktree
- **THEN** that background session is stopped before the resume launches

#### Scenario: Unrelated background session is left alone

- **WHEN** Claude Code reports a live background session under the worktree whose session id differs from the task's
- **THEN** it is not stopped

#### Scenario: Fresh start or non-Claude backend

- **WHEN** a task is started without resume, or its backend is not Claude
- **THEN** no background-session check runs

#### Scenario: Check failure does not block resume

- **WHEN** the `claude` CLI is unavailable or listing/stopping fails
- **THEN** the failure is logged and the resume proceeds
