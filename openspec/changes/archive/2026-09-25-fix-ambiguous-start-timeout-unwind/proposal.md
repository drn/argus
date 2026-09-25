## Why

A previous change (`slow-prelaunch-rest-client-deadlines`'s merged half) addressed the risk of `CreateAndStart` deleting a freshly created worktree during a slow pi prelaunch by giving `StartSession` a flat 7-minute RPC deadline for every backend, at both the TUI-to-daemon and daemon-to-supervisor hops. That fixes the destructive-unwind risk for pi but does so by making every backend — including Claude, which never has slow prelaunch work — wait up to 7 minutes before a genuinely dead daemon is reported as unreachable, instead of failing fast. A concurrently developed change (`fix-pi-backend-start-timeout`) narrowed the longer deadline back to pi specifically via a per-backend timeout table, which reopens the original race for backends with prelaunch work that can legitimately exceed even that shorter, backend-scoped budget (e.g. a genuinely cold pi/ollama model load, whose bounded prelaunch budget is 6 minutes, well past a per-backend RPC deadline sized for the common case).

The actual defect was never the RPC deadline — it was `agent.CreateAndStart`'s compensating-action stack treating *any* `runner.Start` error, including an ambiguous client-side timeout, as proof the daemon-side start failed and grounds to delete the worktree and task row. The client giving up waiting does not stop the daemon from continuing to start the session in the background (`callWithTimeout`'s dispatch goroutine keeps the RPC running to completion regardless of the caller's timeout). Fixing that distinction directly — rather than stretching every RPC deadline to try to outrun it — removes the destructive consequence regardless of how a given backend's prelaunch is timed.

## What Changes

- Add `agent.ErrStartAmbiguous`, a sentinel any `SessionProvider.Start` implementation can wrap to mean "the caller's wait was abandoned, not that the daemon-side start definitively failed."
- `Client.Start` (`internal/daemon/client`) wraps a client-side RPC timeout specifically (not other RPC errors, which do mean the daemon is genuinely unreachable) in `agent.ErrStartAmbiguous`.
- `agent.CreateAndStart` checks `errors.Is(err, agent.ErrStartAmbiguous)` and skips its destructive unwind (worktree removal + task row deletion) for that case, leaving the task `Pending` and the worktree intact instead, with a distinguishing error message.
- This supersedes the flat 7-minute `StartSession` deadline from the prior change: per-backend RPC deadlines (see `fix-pi-backend-start-timeout`) are now safe to keep short, since a timeout no longer destroys state — it just means the creator doesn't yet know the outcome.

## Capabilities

### Modified Capabilities

- `agent-execution`: "Slow prelaunch does not time out task startup" is redefined around the ambiguous-timeout distinction rather than a long RPC deadline.

## Impact

- `internal/agent/iface.go`, `internal/agent/create.go`, `internal/daemon/client/client.go`.
- Known gap (unchanged from before): nothing currently promotes a `Pending` task back to `InProgress` if the daemon-side start actually succeeds after the client already gave up and returned an ambiguous error. The user has to notice (task still shows in the list, worktree intact) and retry, or the daemon-side session simply sits there until some other reconciliation notices it. Not solved here; named as a follow-up, same as the prior change named its own web/macOS timeout-parity follow-up.
- OpenSpec remains local documentation; `make pre-pr` remains the quality gate.
