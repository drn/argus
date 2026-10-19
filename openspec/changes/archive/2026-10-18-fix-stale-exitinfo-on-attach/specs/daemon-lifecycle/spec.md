## ADDED Requirements

### Requirement: Stale exit info is discarded when a stream attaches to a live session

A cached ExitInfo SHALL belong to the incarnation that produced it. When a stream attaches to a live session, the core SHALL discard any cached ExitInfo for that task that predates the attach, so a previous incarnation's entry (notably a kick or recycle restart's `PendingRestart=true` info) cannot be served as the replacement's exit. An entry observed while the session is already done SHALL be kept, since it may be that session's own.

#### Scenario: Recycle leaves an unconsumed exit info

- **WHEN** a recycle replaced a session and a stream then attaches to the replacement
- **THEN** the old incarnation's cached ExitInfo is removed, and the replacement's later exit yields its own ExitInfo with `PendingRestart=false`
