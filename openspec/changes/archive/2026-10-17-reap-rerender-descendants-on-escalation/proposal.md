## Why

Killing only an unresponsive agent leaves its helpers alive and can hold the PTY slave open for another five seconds. Aaron explicitly accepted stopping agent-started dev servers when a width repair must escalate.

## What changes

- Preserve descendants when the agent exits cooperatively during its three-second SIGTERM grace.
- On escalation, capture the agent's descendant tree, including children in separate sessions, then terminate those descendants with a short SIGTERM-to-SIGKILL grace.
- Restrict signals to captured process identities and log descendant counts through the production logger.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: escalation-only descendant cleanup.

## Impact

Agent shutdown, supervisor stream identity, tests and gotchas. No REST contract changes.
