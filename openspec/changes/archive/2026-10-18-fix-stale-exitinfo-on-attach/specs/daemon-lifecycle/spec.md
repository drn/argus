## ADDED Requirements

### Requirement: Stale exit info is discarded when a stream attaches to a live session

A cached ExitInfo SHALL belong to the incarnation that produced it. When a stream attaches to a live session, the core SHALL discard any cached ExitInfo for that task that predates the attach, so a previous incarnation's entry (notably a kick or recycle restart's `PendingRestart=true` info) cannot be served as the replacement's exit. Ownership SHALL be identity-based: each cached ExitInfo is stamped with the session that produced it, an entry owned by another session is stale, and an entry owned by the attaching session SHALL be kept even if that session already exited. Waiting for a session's exit info SHALL accept only an entry it owns. An entry with no recorded owner (including a restart-failure re-fire for a task with no live session) is dropped only if the attaching session is still running, and SHALL still be delivered to a reader. Waiting SHALL NOT run to its bound when the session's own entry was already consumed or was overwritten by a later incarnation.

#### Scenario: Recycle leaves an unconsumed exit info

- **WHEN** a recycle replaced a session and a stream then attaches to the replacement
- **THEN** the old incarnation's cached ExitInfo is removed, and the replacement's later exit yields its own ExitInfo with `PendingRestart=false`
