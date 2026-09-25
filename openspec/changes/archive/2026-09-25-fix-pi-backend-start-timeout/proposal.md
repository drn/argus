## Why

A Pi-backend task launch was investigated after appearing to fail with `daemon RPC call timed out`. The daemon log showed the real sequence: `agent.EnsurePrelaunch` detected the pi backend and spent ~11s bringing up ollama before `runner.Start` continued, while `Client.Start`'s RPC call to `Daemon.StartSession` uses the flat 2s `rpcTimeout` shared by every other RPC. The client gave up and reported a timeout well before the daemon-side prelaunch (and the actual `BuildCmd` failure that followed it, unrelated to timing) ever got a chance to respond. The 2s budget is correct for ordinary RPCs but was never sized for a backend with real prelaunch work, and there was no mechanism to special-case it short of hardcoding a branch in `Start`.

## What Changes

- Give `Client.Start` a per-backend RPC timeout: resolve the task's backend and, for pi, use a longer budget (20s) instead of the default 2s `rpcTimeout`, mirroring the existing `UpdateSelf` / `updateSelfTimeout` pattern for legitimately long-running RPCs.
- Structure the lookup as a small ordered table (`startTimeoutOverrides`) keyed by the same `Is<Backend>Backend` predicates the rest of the codebase already uses for backend detection, so a future backend needing more headroom is a one-line addition rather than a new conditional in `Start`.
- Unresolvable/default backends keep the existing 2s behavior — this only widens the budget for pi.

## Capabilities

### Modified Capabilities

- `daemon-client`: `Client.Start`'s RPC deadline is now backend-aware instead of a single flat constant for all backends.

## Impact

- `internal/daemon/client/client.go` only. This is a local Unix-socket RPC client timeout, not a wire-contract or REST change — the web/macOS/remote-TUI surfaces don't go through this code path (REST handlers call `runner.Start` in-process with no artificial deadline; `apiclient`'s HTTP client already uses a separate 30s timeout), so no other frontend needs a matching change.
- Purely a client-side wait budget: the daemon-side RPC handler and prelaunch work are unaffected either way and keep running to completion in the background regardless of which deadline the client is using (see `callWithTimeout`'s drain-on-timeout goroutine).
- OpenSpec remains local documentation; `make pre-pr` remains the quality gate.
