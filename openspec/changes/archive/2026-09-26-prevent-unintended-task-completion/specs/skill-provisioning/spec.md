## ADDED Requirements

### Requirement: Explicit agent completion intent

The bundled `argus-complete` skill SHALL instruct an agent to change its Argus task to `complete` only when the user explicitly requests that status change. Completion of the underlying work, a merged PR, and a final response SHALL NOT be treated as authorization. The installed skill SHALL carry the bundled instruction. If the `task_complete` tool is unavailable or errors, the skill SHALL stop and report that condition; it SHALL NOT discover or call a local HTTP endpoint as a fallback.

#### Scenario: Work finished without status request

- **WHEN** an agent finishes the requested work but the user did not ask it to mark the Argus task Complete
- **THEN** the skill instructs the agent to leave task status unchanged

#### Scenario: Tool unavailable

- **WHEN** the user explicitly requested completion but `task_complete` is not exposed to the agent
- **THEN** the skill instructs the agent to report that it cannot complete the status change without trying another transport
