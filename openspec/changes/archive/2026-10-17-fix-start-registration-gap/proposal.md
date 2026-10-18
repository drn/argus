## Why

`Runner.Start` execs the agent process before publishing it in `r.sessions`. During that gap (and the kick-restart gap before the restart's `Start` reserves the slot), `Daemon.WriteInput` and `Daemon.Resize` answered "session not found", silently dropping a client's input or resize right after a kick restart. It also made `TestSupResumeID` flake in CI.

## What Changes

- `Runner.GetStarting` waits (bounded) for an in-flight start or queued kick-restart to publish its session; the supervisor/daemon `WriteInput` and `Resize` handlers use it.

## Impact

- Behavior-only; no RPC surface change. Other lookups (Get, SessionStatus) are unchanged.
