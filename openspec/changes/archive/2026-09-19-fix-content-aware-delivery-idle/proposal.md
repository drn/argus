## Why

Hera self-service recycle and reliable pane delivery currently gate on raw PTY-byte silence, so cosmetic status-line redraws can keep a genuinely parked session “busy” indefinitely. A live incident left both a pending recycle and a queued Hera message stalled for 56 minutes despite no meaningful agent progress.

## What Changes

- Expose a reusable, stateful content-aware idle classifier for low-frequency daemon consumers.
- Use content-aware idle for self-service recycle and reliable pane delivery while preserving immediate raw-idle behavior.
- Log when a pending recycle has remained non-idle for at least two minutes.
- Document the shared idle-gate failure mode and the required detector semantics.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `idle-detection`: Make the existing content-aware signal reusable by daemon gates and cover primary-screen scrollback.
- `reliable-pane-delivery`: Allow delivery once either raw or content-aware idle is established.
- `coordinator-context-management`: Allow self-service recycle once either raw or content-aware idle is established and expose prolonged waits in logs.

## Impact

Affected code is limited to `internal/agent`, `internal/daemon/recycle.go`, `internal/notify`, and `internal/hera/recycle_watcher.go`, plus their tests and the idle-detection gotcha documentation. No REST contract, persistent schema, frontend surface, or external dependency changes.
