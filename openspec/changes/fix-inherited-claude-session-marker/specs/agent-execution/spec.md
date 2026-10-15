## ADDED Requirements

### Requirement: Spawn environment strips a parent Claude Code session's runtime markers

Every agent spawn, for every backend, SHALL remove a parent Claude Code session's per-session runtime markers from the environment it inherits from the spawning process: `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_SESSION_ATTENDED`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_PID`, and `CLAUDE_EFFORT`. Other inherited variables, including user-configured Claude settings variables, SHALL be kept. A backend `env_vars` mapping that explicitly sets one of these names SHALL still apply. Stripped names SHALL be logged, never their values.

#### Scenario: Supervisor started from inside Claude Code

- **WHEN** the spawning process's environment contains `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION=1`, and a Claude agent is spawned
- **THEN** the agent's environment contains neither variable

#### Scenario: Markers are stripped for non-Claude backends too

- **WHEN** the spawning process's environment contains `CLAUDE_CODE_SESSION_ID` and `CLAUDE_CODE_MESSAGING_TOKEN`, and a Codex agent is spawned
- **THEN** the agent's environment contains neither variable

#### Scenario: User-configured Claude settings variables are kept

- **WHEN** the spawning process's environment contains `CLAUDE_CODE_ENABLE_TELEMETRY=1`
- **THEN** the spawned agent's environment still contains `CLAUDE_CODE_ENABLE_TELEMETRY=1`

#### Scenario: Backend env_vars still win

- **WHEN** a backend's `env_vars` explicitly sets `CLAUDE_EFFORT`
- **THEN** the agent's environment contains that configured value

#### Scenario: Stripping is logged by name only

- **WHEN** markers are stripped for a spawn
- **THEN** a log line names the stripped variables and contains none of their values

### Requirement: Claude agents always persist transcripts

Every Claude-family agent spawn SHALL export `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`, so Claude Code keeps saving the session transcript that Argus relies on for resume, session discovery and coordinator context tracking. Non-Claude backends SHALL NOT receive this variable from Argus.

#### Scenario: Claude agent gets the persistence flag

- **WHEN** a Claude-family agent is spawned
- **THEN** its environment contains `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`

#### Scenario: Codex agent does not get the persistence flag

- **WHEN** a Codex agent is spawned from an environment without that variable
- **THEN** its environment does not contain `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE`
