## ADDED Requirements

### Requirement: The Claude usage probe never runs as a child session

The headless Claude `/usage` probe SHALL run with the same parent-session markers removed as an agent spawn (see `agent-execution`'s "Spawn environment strips a parent Claude Code session's runtime markers") and with `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`, so a daemon started from inside Claude Code never probes as a child of that session.

#### Scenario: Daemon started from inside Claude Code

- **WHEN** the daemon's environment contains `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION=1` and the Claude usage probe runs
- **THEN** the probe process's environment contains neither variable and contains `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`
