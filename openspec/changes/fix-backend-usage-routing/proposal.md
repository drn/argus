## Why

Tiered backend routing has never actually switched a single task off Claude. The Claude `/usage` probe waits for an interactive `claude` session to exit (it never does), so every tick times out and the cache stays empty; its failure logs go through `uxlog`, a no-op in the daemon, so nobody could see it. The Codex probe only trusts a rollout file touched in the last hour and reads the wrong rate-limit window, so it is also almost always "unknown". With both unknown, fail-open routing picks tier 1 (Claude) every time — Aaron hit 100% weekly Claude usage with zero spill to Codex. Separately, the only strategy is fill-tier-1-then-tier-2; Aaron wants load spread across backends (Claude reserved for coordinators via its own threshold).

## What Changes

- Claude usage probe: run in a dedicated empty argus-owned directory, answer that directory's one-time folder-trust dialog, stream the PTY screen and stop as soon as the weekly line parses (no wait-for-exit), parse the real `/usage` layout (percentage may be on the line after the "Current week (all models)" header), and log outcomes via `slog` so they reach `daemon.log`.
- Codex usage probe: identify the weekly window by `window_minutes == 10080` (it is `primary` today, not `secondary`), read only that window, treat `rate_limit_reached_type` set with no window data as 100%, and trust the latest rollout reading until its own `resets_at` instead of a 1-hour mtime window.
- Both probes route on the weekly window only (5-hour windows ignored, by decision).
- New `[backend_routing].strategy` config.toml key: `"ordered"` (default, today's behavior) or `"headroom"` — pick the tier with the most headroom (`threshold_pct − weekly used %`), preferring any backend with a known reading over one with an unknown reading; all-unknown falls back to list order.
- **BREAKING (semantics):** ordered mode's fail-open is unchanged, but a Codex reading now stays valid for days, so a depleted Codex account will correctly stop receiving work.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `backend-tier-routing`: adds the `strategy` setting + headroom resolution; changes Codex probe window selection, freshness, and depleted handling; probe logs reach the daemon log.
- `usage-budget-routing`: Claude probe streaming/early-stop, dedicated probe directory + trust-dialog handling, real-layout parsing, daemon-visible logging.

## Impact

- Code: `internal/usagebudget/usagebudget.go`, `internal/backendtier/{resolver,codexprobe}.go`, `internal/config/config.go`, `internal/daemon/daemon.go` (probe at startup, not only after the first 30-minute tick).
- No REST/wire change; status-bar usage display (`BootInfo`) starts showing real numbers. Settings UI surface for `strategy` is a named follow-up (config.toml-only v1, same as `codex_pty_fallback_enabled`).
- Hera coordinator carve-out (`resolveCoordinatorBackend`) untouched.
