# usage-budget-routing Specification

## Purpose

Usage budget routing steers Hera worker/freelance spawns away from Claude when the account-wide weekly Claude budget is manually protected or crosses a configured threshold.

## Requirements
### Requirement: Weekly usage percentage is probed and cached, never checked live

The system SHALL periodically sample Aaron's current weekly Claude usage percentage and reset timestamp via a background probe, and SHALL cache the result (percentage, reset timestamp, last-probed-at) in memory for synchronous reads. The system SHALL NOT perform a live probe synchronously inside a spawn call; every read of the cached value SHALL be non-blocking regardless of probe freshness.

#### Scenario: Cache read never blocks a spawn

- **WHEN** a hera worker/freelance spawn resolves its backend
- **THEN** the resolution reads only the in-memory cached usage percentage, never triggering a live probe

#### Scenario: Cache starts empty after a daemon restart

- **WHEN** the daemon has just started and no probe tick has completed yet
- **THEN** the cached usage percentage is treated as unknown, and any budget-aware resolution behaves as if the switch were inactive

### Requirement: The usage probe fails open on any error

The system SHALL treat any probe failure — process error, timeout, or unparseable output — as "unknown," leaving the previous cached value in place (or empty, if none exists yet) rather than raising an error or blocking daemon startup. The system SHALL log probe failures via uxlog without surfacing them as user-facing errors.

#### Scenario: Probe subprocess fails

- **WHEN** the probe's subprocess exits non-zero or times out
- **THEN** the cached value is left unchanged and the failure is logged, with no error propagated to any caller

#### Scenario: Probe output does not parse

- **WHEN** the probe's rendered output does not match the expected weekly-usage format
- **THEN** the cache is left unchanged and a parse-failure warning is logged

### Requirement: Budget-aware backend resolution for worker/freelance spawns only

The system SHALL provide a resolution function consulted ONLY by hera worker/freelance role-spawn paths (`hera_spawn_worker`'s MCP handler and the plan-DAG gater's leaf-worker materialization path), and SHALL NOT be consulted by any coordinator or sub-coordinator spawn path. Given an explicit caller-supplied backend, the function SHALL return it unchanged without reading the usage cache. Given no explicit backend, the function SHALL return the configured fallback backend (default `"codex"`) when the switch is active, and SHALL return empty (deferring to the existing project/default backend precedence) otherwise.

#### Scenario: Explicit backend always wins

- **WHEN** hera_spawn_worker is called with an explicit `backend`
- **THEN** the budget-aware resolver is not consulted and the explicit backend is used unchanged

#### Scenario: Manual switch active with no explicit backend

- **WHEN** the manual "always fallback" switch is enabled and a worker/freelance role is spawned with no explicit backend
- **THEN** the spawn resolves to the configured fallback backend

#### Scenario: Threshold crossed activates the fallback

- **WHEN** the threshold switch is configured at a given percentage and the cached weekly-usage percentage is at or above it
- **THEN** a worker/freelance spawn with no explicit backend resolves to the configured fallback backend

#### Scenario: Threshold not crossed leaves default precedence untouched

- **WHEN** the threshold switch is configured but the cached weekly-usage percentage is below it (or unknown)
- **THEN** the resolver returns empty and the spawn falls through to the existing project/default backend precedence

#### Scenario: Fallback automatically deactivates after the weekly reset

- **WHEN** real time passes the cached reset timestamp and a subsequent probe reads a lower usage percentage below the configured threshold
- **THEN** the threshold-triggered fallback stops applying, with no separate expiry timer or manual reset required

#### Scenario: Coordinator and sub-coordinator spawns are never affected

- **WHEN** a coordinator role is created via `hera_new_orchestrator`, or a plan-DAG `subcoord` node is materialized
- **THEN** the budget-aware resolver is never consulted, regardless of the switch or threshold configuration

### Requirement: The switch is a global, config.toml-only setting

The system SHALL read the manual switch, threshold percentage, and fallback backend from a global `[hera.worker_budget]` table in `~/.argus/config.toml`, not from any per-project configuration, since weekly usage is a single account-wide resource shared across every project's spawns.

#### Scenario: Setting applies across all projects

- **WHEN** the global switch or threshold is configured
- **THEN** it applies to worker/freelance spawns in every project, not just the project where it was set

#### Scenario: Missing or invalid config leaves the feature inactive

- **WHEN** the `[hera.worker_budget]` table is absent, or names a fallback backend not present in `cfg.Backends`
- **THEN** the budget-aware resolver behaves as inactive (always returns empty), never erroring the spawn
