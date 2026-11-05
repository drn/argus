## Why

The TUI froze typing for ~20s at a time. The per-tick Hera rail refresh annotates every role via `resolveHeraTier`, which calls tier resolution, which (since #1110) made a synchronous `Daemon.RoutingUsage` RPC on the UI goroutine: one per role per tick (each ~30ms when it succeeds; the 2s client deadline is only the cap).

## What Changes

- The TUI wraps the daemon usage RPC in a stale-while-revalidate cache (`backendtier.CachedUsage`): reads return the last snapshot immediately and refresh in a single background goroutine. Primed once at startup, before the UI loop.
- Readings may now be up to five minutes stale (refreshed at most once a minute); a snapshot older than 5 minutes, or none yet, falls back to local caches exactly like a failed RPC.

## Capabilities

### Modified Capabilities

- `backend-tier-routing`: the TUI's daemon-reading requirement no longer implies a synchronous read per resolution.

## Impact

- `internal/backendtier`, `cmd/argus/main.go`. No wire, REST, or web/macOS change (those resolve daemon-side). Daemon-side slowness of `RoutingUsage` (per-account `BridgeUsage` DB lookups plus `db.Config()` under SQLite contention) is a named follow-up.
