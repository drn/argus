# usage-budget-routing Specification

## Purpose

Provides the account-wide weekly Claude usage probe and in-memory cache that `backend-tier-routing`'s tier-list resolver reads (`claude_usage` probe kind). This capability no longer performs any backend resolution of its own — that was retired by `fix-backend-routing-semantics` (see `backend-tier-routing` and `hera-coordination` for the current resolution semantics) — it owns only the probe and its cache.
## Requirements
### Requirement: Weekly usage percentage is probed and cached, never checked live

The system SHALL periodically sample Aaron's current weekly Claude usage percentage and reset timestamp via a background probe, and SHALL cache the result (percentage, reset timestamp, last-probed-at) in memory for synchronous reads. The system SHALL NOT perform a live probe synchronously inside a spawn call; every read of the cached value SHALL be non-blocking regardless of probe freshness.

#### Scenario: Cache read never blocks a backend resolution

- **WHEN** `backend-tier-routing`'s resolver reads this cache for its `claude_usage` probe kind
- **THEN** the read reaches only the in-memory cached usage percentage, never triggering a live probe

#### Scenario: Cache starts empty after a daemon restart

- **WHEN** the daemon has just started and no probe tick has completed yet
- **THEN** the cached usage percentage is treated as unknown, and any tier naming the `claude_usage` probe kind is treated as available (fail-open)

### Requirement: The usage probe fails open on any error

The system SHALL treat any probe failure — process error, timeout, or unparseable output — as "unknown," leaving the previous cached value in place (or empty, if none exists yet) rather than raising an error or blocking daemon startup. The system SHALL log probe failures via uxlog without surfacing them as user-facing errors.

#### Scenario: Probe subprocess fails

- **WHEN** the probe's subprocess exits non-zero or times out
- **THEN** the cached value is left unchanged and the failure is logged, with no error propagated to any caller

#### Scenario: Probe output does not parse

- **WHEN** the probe's rendered output does not match the expected weekly-usage format
- **THEN** the cache is left unchanged and a parse-failure warning is logged

### Requirement: The Claude usage probe never runs as a child session

The headless Claude `/usage` probe SHALL run with the same parent-session markers removed as an agent spawn (see `agent-execution`'s "Spawn environment strips a parent Claude Code session's runtime markers") and with `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`, so a daemon started from inside Claude Code never probes as a child of that session.

#### Scenario: Daemon started from inside Claude Code

- **WHEN** the daemon's environment contains `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION=1` and the Claude usage probe runs
- **THEN** the probe process's environment contains neither variable and contains `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`

