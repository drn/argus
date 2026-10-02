## ADDED Requirements

### Requirement: Explicit agent request for task completion

The `task_complete` tool description SHALL tell agents to call it only in response to an explicit request to mark the Argus task Complete, not as automatic cleanup after work finishes. Once deliberately invoked, its existing status-write, timestamp, idempotency, and session behavior SHALL be unchanged.

#### Scenario: Tool discovery after ordinary work

- **WHEN** an agent discovers `task_complete` while working on a request that does not ask for an Argus status change
- **THEN** the tool description instructs the agent to leave the status unchanged
