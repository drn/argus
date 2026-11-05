## Why

A Hera coordinator created from the TUI landed on the default Claude account at 98% weekly usage and hit a 429 within minutes, while the personal account sat at 70%. TUI-initiated task creation runs tier resolution inside the TUI process, whose usage caches (`usagebudget`, Codex) are only populated in the daemon. Every tier read as unknown, so headroom routing returned the first unread tier. A second hole: when every known tier was over threshold, resolution returned empty and callers fell back to the default backend on the default account.

## What Changes

- Tier resolution reads usage through an injectable source. The TUI installs a daemon RPC (`Daemon.RoutingUsage`: Claude per account plus Codex) at startup when connected to the daemon; one RPC per resolution. No daemon, or an RPC failure, falls back to local caches and logs via uxlog.
- Under the `headroom` strategy, when every known tier is at/over its threshold and none is unread, resolution picks the tier with the lowest usage percent instead of returning empty. Reason is logged. `ordered` is unchanged.
- Regression coverage that a missing reading ranks below a real reading with headroom and above a real reading over threshold.

## Capabilities

### Modified Capabilities

- `backend-tier-routing`: usage readings come from the daemon in the TUI process; headroom all-over-threshold fallback.

## Impact

- `internal/backendtier`, `internal/daemon/{rpc.go,client}`, `cmd/argus/main.go`. New additive RPC method (older daemon: call errors, TUI falls back to local caches).
- Frontend parity: web and macOS create tasks over REST, so routing already runs daemon-side; `--remote` TUI likewise. No change there.
