## Why

Claude's bridge now reports facts that terminal scraping can only infer. Phase 2 makes those reports authoritative while preserving automatic fallback when the mod is absent, stale, or belongs to an earlier session.

## What Changes

- Bind bridge liveness to the currently running session and use live state for daemon and TUI idle and needs-input verdicts.
- Prefer fresh account-scoped rate limits over hidden usage probes.
- Persist context and cumulative cost from measurements, retaining transcript fallback for sessions without a live bridge.
- Keep scraper disagreement diagnostics and existing fallback regression behavior.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `argus-bridge`: authoritative, restart-aware liveness and measurement state (extends phase 1's pending capability).
- `idle-detection`: bridge precedence in daemon and daemon-connected TUI.
- `usage-budget-routing`: fresh account-scoped measurement preference.
- `coordinator-context-management`: measurement-based context reporting.
- `cost-estimation`: cumulative bridge cost accrual with reset handling.
- `rest-api`: batch bridge snapshot for remote TUI parity.

## Impact

Bridge hub, REST watcher/read endpoints, daemon client/TUI polling, usage routing, binding persistence, and coordinator Stop hook. Web and macOS consume the daemon's existing status events; no new client UI is required. Message delivery and budget nudge replacement remain phase 3. Neither phase 1 nor phase 2 is archived until Aaron runs the result.

Named-account headless fallback (probe per account) is a follow-up; missing named-account bridge measurements remain unknown and fail open.
