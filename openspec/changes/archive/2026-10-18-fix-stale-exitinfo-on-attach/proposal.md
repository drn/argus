## Why

TestSupResumeID flaked in CI with a task left `in_progress` after Stop. Root cause is production: a kick/recycle restart caches the old incarnation's ExitInfo (`PendingRestart=true`) and nothing consumes it, because clients treat the restart gap as alive and reconnect. At the replacement's real exit that stale entry satisfies `awaitExitInfoCached`, so the relay can consume it instead of the fresh one; the daemon then skips the exit-time status flip and the task stays `in_progress` with no agent.

## What changes

- A stream attaching to a live session discards any cached ExitInfo that predates it.
- Supervisor stream surface bumped to 13 (supervisor-side behavior change).

## Non-goals

No wire/ProtocolVersion change; no change to ExitInfo contents or to the consume-once rule.
