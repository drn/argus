# backend-tier-routing Specification

## Purpose
TBD - created by archiving change add-tiered-backend-routing. Update Purpose after archive.
## Requirements
### Requirement: Ordered tier list drives default backend resolution

The system SHALL support an ordered list of backend tiers, each naming a backend that must exist in the configured backend roster and a probe kind (`claude_usage`, `codex_usage`, or `none`). The resolver SHALL walk the list in order and return the first tier that is available: an uncapped (`none`) tier is always available; a capped tier is available when its probe's cached usage percentage is below its configured threshold, or when the cached reading is stale or unknown (fail open). If no tier list is configured, or every capped tier is over threshold and no uncapped tier exists in the list, the resolver SHALL return empty, deferring to the existing single default-backend precedence.

#### Scenario: First tier under threshold is selected

- **WHEN** the tier list has a capped first tier below its threshold
- **THEN** the resolver returns that tier's backend without consulting later tiers

#### Scenario: First tier over threshold falls through to the next

- **WHEN** the first tier is capped and at or above its threshold, and a second tier is below its threshold (or uncapped)
- **THEN** the resolver returns the second tier's backend

#### Scenario: All capped tiers exhausted falls through to an uncapped tier

- **WHEN** every capped tier in the list is at or above its threshold and the list contains a `none`-probe tier
- **THEN** the resolver returns the uncapped tier's backend

#### Scenario: No tier list configured

- **WHEN** no tiers are configured
- **THEN** the resolver returns empty and the caller falls back to the existing single default-backend precedence

#### Scenario: Stale or unknown usage reading is treated as available

- **WHEN** a capped tier's usage probe has no cached reading yet, or the cached reading is older than the probe's staleness window
- **THEN** that tier is treated as available (not over threshold) rather than blocking resolution

### Requirement: Probe kinds are dispatched by kind string, independent of backend name

The system SHALL resolve a tier's usage percentage by dispatching on the tier's `probe` field (`claude_usage`, `codex_usage`, `none`), independent of which backend name the tier carries. Each kind's probe implementation MAY be hardcoded, provider-specific code — no generic prober interface or runtime plugin registration is required. Adding a new probe kind SHALL be a small, contained addition (a new dispatch case plus a new hardcoded probe implementation), not a change to the tier-list resolution loop itself.

#### Scenario: Unknown probe kind is treated as uncapped-unavailable, not an error

- **WHEN** a tier names a probe kind with no matching dispatch case
- **THEN** the tier is skipped (treated as never available) and resolution continues to the next tier, without erroring the caller

### Requirement: Claude usage probe (existing behavior, reused)

The system SHALL determine Claude usage via the existing headless `/usage` PTY probe and in-memory cache, fail-open on any probe or parse error exactly as today's hera-scoped probe behaves, and cache percentage + reset timestamp for synchronous, non-blocking reads by the tier resolver.

#### Scenario: Cache read never blocks resolution

- **WHEN** the tier resolver reads the Claude probe's cached usage percentage
- **THEN** it never triggers a live probe synchronously

### Requirement: Codex usage probe prefers a free local-file read, falls back to a costed live probe

The system SHALL determine Codex usage primarily by reading the `rate_limits.used_percent` field from the most recent Codex CLI rollout log file (`~/.codex/sessions/**/rollout-*.jsonl`), without spawning any subprocess, when a rollout file newer than the probe's staleness window exists. When no sufficiently fresh rollout file exists, the system SHALL fall back to a headless `codex` PTY probe that sends a minimal message and reads the rendered `/status` output, on the same background cadence as the Claude probe. Both paths SHALL fail open: any read, parse, or subprocess error leaves the previous cached value in place (or unknown, if none exists) and logs via uxlog without erroring the caller.

#### Scenario: Fresh rollout file satisfies the probe with no subprocess

- **WHEN** a Codex rollout log newer than the staleness window contains a `rate_limits` record
- **THEN** the probe reads `used_percent` from that file and does not spawn a `codex` subprocess

#### Scenario: No fresh rollout file falls back to the live PTY probe

- **WHEN** no Codex rollout log newer than the staleness window exists
- **THEN** the probe spawns a headless `codex` session, sends a minimal message, and parses the rendered `/status` output

#### Scenario: Rollout file exists but has no rate_limits record

- **WHEN** the most recent rollout file has no `rate_limits` field in any record
- **THEN** the probe treats this the same as no fresh file and falls back to the live PTY probe

#### Scenario: Live probe fails

- **WHEN** the fallback `codex` PTY probe's subprocess errors, times out, or its output does not parse
- **THEN** the cache is left unchanged (or unknown) and the failure is logged, with no error propagated to any caller

### Requirement: Tier list configuration source precedence

The system SHALL read the tier list from `config.toml`'s `[backend_routing]` table when at least one `[[backend_routing.tier]]` entry is present, treating that list as authoritative in full. When `config.toml` defines no tier list, the system SHALL read a tier list persisted via the Settings UI (a structured DB-backed store) instead. The two sources SHALL NOT be merged.

#### Scenario: config.toml tier list is authoritative

- **WHEN** `config.toml` defines one or more tiers under `[backend_routing]`
- **THEN** the resolver uses exactly that list, ignoring any DB-persisted tier list

#### Scenario: DB tier list used when config.toml defines none

- **WHEN** `config.toml` has no `[backend_routing]` table or an empty tier list
- **THEN** the resolver uses the tier list persisted via the Settings UI, if any

### Requirement: A misconfigured tier is skipped, never fatal

The system SHALL skip, rather than error on, a tier whose backend name is not present in the configured backend roster, treating it as never available. A tier list containing only misconfigured tiers SHALL behave identically to no tier list being configured.

#### Scenario: Tier names a nonexistent backend

- **WHEN** a tier's backend name is not present in `cfg.Backends`
- **THEN** that tier is skipped during resolution and never returned

#### Scenario: Every tier misconfigured degrades to default precedence

- **WHEN** every configured tier names a nonexistent backend
- **THEN** the resolver returns empty and the caller falls back to the existing single default-backend precedence

