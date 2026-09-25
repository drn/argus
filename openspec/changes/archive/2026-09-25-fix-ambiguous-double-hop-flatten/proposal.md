## Why

Code review of `fix-ambiguous-start-timeout-unwind` (not yet merged) found a real gap in that fix: `agent.ErrStartAmbiguous` only survived the TUI-to-daemon RPC hop. In P4 supervisor mode, the daemon's own `sessionCore.StartSession` makes a SECOND `Client.Start` RPC hop to the supervisor. If that inner call times out, the error already wraps `ErrStartAmbiguous` — but `StartSession`'s handler flattened every error into a plain `resp.Error` string, and the outer `Client.Start` only re-wrapped on its OWN timeout (`errors.Is(err, ErrRPCTimeout)`), never on `resp.Error != ""`. So a daemon reply built from an ambiguous INNER timeout would reach `CreateAndStart` looking like a definitive failure and get unwound — deleting the worktree/task exactly the case the original fix exists to prevent. In practice this was masked because the outer hop's clock starts strictly before the inner one's (same per-backend duration at both hops today), so the outer timeout fires first — but that's an implicit, timing-dependent property, not a contract, and nothing stops a future divergence from exposing it.

## What Changes

- `daemon.StartResp` gains an `Ambiguous bool` field (additive jsonrpc field, zero-value-safe both directions).
- `sessionCore.StartSession` sets it via `errors.Is(err, agent.ErrStartAmbiguous)` when its own `runner.Start` call fails.
- The outer `Client.Start` checks `resp.Ambiguous` on the `resp.Error != ""` branch and re-wraps in `agent.ErrStartAmbiguous` when set, so the distinction survives regardless of which hop's timeout actually fired.
- Bump `SupervisorStreamSurface` (2 → 3) — `sessioncore.go` is on the stream-surface manifest and this is a genuine behavior change a stale supervisor won't exhibit.
- New deterministic end-to-end test (`TestSupInnerAmbiguous`) constructs the double-hop scenario without racing real timers: the inner hop resolves the default short timeout for a registered non-pi backend, while the outer hop is given a fabricated (never-wire-sent) cfg that resolves the same backend name to pi, so the inner hop times out first by construction, not by chance.

## Capabilities

### Modified Capabilities

- `agent-execution`: "Slow prelaunch does not time out task startup" gains a scenario covering the double-hop case explicitly.

## Impact

- `internal/daemon/types.go`, `internal/daemon/sessioncore.go`, `internal/daemon/client/client.go`, `internal/daemon/surface.go`, plus the new test.
- Verified test-quality by temporarily reverting the fix and confirming `TestSupInnerAmbiguous` fails without it (not a test that would pass regardless of the wiring).
