## Why

Hera participants waiting for another role currently have to poll `hera_inbox` themselves, which encourages background timer loops that consume an LLM turn on every check. The existing task-message bus already proves that a bounded server-side wait can avoid that waste while keeping daemon shutdown responsive.

## What changes

- Add an optional `timeout_seconds` argument to `hera_inbox`; values from 1 through 120 block server-side until unread mail arrives or the timeout elapses, while the existing omitted/zero behavior remains immediate.
- Reuse Hera's unread-inbox semantics, including the `read_at IS NULL` convention, delivery cancellation, and read acknowledgement after a waiting call returns messages.
- Document blocking `hera_inbox` as the recommended way for a Hera role to await a reply instead of hand-rolled timer polling.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `hera-messaging`: Extend inbox reads with a bounded, shutdown-aware server-side wait for unread role messages.
- `task-orchestration`: Direct gater-materialized workers to use the blocking inbox wait for their mandatory go/wait check-in.

## Impact

The change affects the Hera message store, service and MCP handler, generated worker orientation, MCP schema tests, Hera messaging tests, the built-in Hera skill, README reference, and messaging gotcha documentation. It adds no schema migration or external dependency and preserves the current non-blocking `hera_inbox` default.
