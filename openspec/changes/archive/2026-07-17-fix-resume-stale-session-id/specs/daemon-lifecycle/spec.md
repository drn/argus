## ADDED Requirements

### Requirement: Resume-time session ID refresh at supervisor-restart reconcile

When the supervisor-restart reconcile flips a task whose live session the supervisor no longer reports (a true orphan) back to a resumable state, the daemon SHALL refresh that task's Claude session ID to the newest transcript for its worktree, so the task's later resume targets the most recent in-place session rather than the stale create-time session. The refresh SHALL be Claude-only and SHALL never corrupt or blank the task row: an orphan with no worktree, no prior session ID, no transcript, or a non-Claude backend keeps its recorded session ID unchanged.

#### Scenario: Orphaned Claude worker refreshed to newest transcript

- **WHEN** the supervisor-restart reconcile orphans a Claude-backed task whose worktree holds a newer transcript than its recorded session ID
- **THEN** the daemon refreshes the task's recorded session ID to the newest transcript UUID so its next resume targets that session

#### Scenario: Non-Claude orphan is left unchanged

- **WHEN** the supervisor-restart reconcile orphans a codex, pi, or opencode task
- **THEN** the daemon leaves the task's recorded session ID unchanged
