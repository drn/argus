## MODIFIED Requirements

### Requirement: Codex usage probe prefers a free local-file read, falls back to a costed live probe

The system SHALL determine Codex usage primarily by reading the `rate_limits.used_percent` field from the most recent Codex CLI rollout log file (`~/.codex/sessions/**/rollout-*.jsonl`), without spawning any subprocess, when a rollout file newer than the probe's staleness window exists. When no sufficiently fresh rollout file exists, the system SHALL fall back to a headless `codex` PTY probe that sends a minimal message and reads the rendered `/status` output, on the same background cadence as the Claude probe, ONLY when that fallback has been explicitly opted into (see "Costed Codex PTY fallback is opt-in and disabled by default" below). When the fallback is not opted into, a stale-or-missing rollout read SHALL leave the cached reading stale/unknown, exactly like any other fail-open probe miss, rather than spending Codex quota. Both paths SHALL fail open: any read, parse, or subprocess error leaves the previous cached value in place (or unknown, if none exists) and logs via uxlog without erroring the caller.

#### Scenario: Fresh rollout file satisfies the probe with no subprocess

- **WHEN** a Codex rollout log newer than the staleness window contains a `rate_limits` record
- **THEN** the probe reads `used_percent` from that file and does not spawn a `codex` subprocess

#### Scenario: No fresh rollout file falls back to the live PTY probe when opted in

- **WHEN** no Codex rollout log newer than the staleness window exists AND the costed PTY fallback is opted into
- **THEN** the probe spawns a headless `codex` session, sends a minimal message, and parses the rendered `/status` output

#### Scenario: No fresh rollout file leaves the cache stale when the fallback is not opted into

- **WHEN** no Codex rollout log newer than the staleness window exists AND the costed PTY fallback has NOT been opted into (the default)
- **THEN** the probe does not spawn a `codex` subprocess, the cache is left exactly as it was (stale or unknown), and the skip is logged via uxlog

#### Scenario: Rollout file exists but has no rate_limits record

- **WHEN** the most recent rollout file has no `rate_limits` field in any record
- **THEN** the probe treats this the same as no fresh file (falling back to the PTY probe, or leaving the cache stale, per the opt-in state above)

#### Scenario: Live probe fails

- **WHEN** the fallback `codex` PTY probe's subprocess errors, times out, or its output does not parse
- **THEN** the cache is left unchanged (or unknown) and the failure is logged, with no error propagated to any caller

## ADDED Requirements

### Requirement: Costed Codex PTY fallback is opt-in and disabled by default

The system SHALL control whether the costed Codex PTY `/status` fallback (above) may ever run via a config.toml-only setting (`[backend_routing].codex_pty_fallback_enabled`), defaulting to `false` when absent. The system SHALL read this setting live on every probe tick, so a config.toml edit takes effect without a daemon restart. This setting SHALL NOT have a DB/Settings-UI surface, matching the config.toml-only v1 scoping the retired `[hera.worker_budget]` switch previously used.

#### Scenario: Absent setting defaults to disabled

- **WHEN** `[backend_routing]` has no `codex_pty_fallback_enabled` key
- **THEN** the costed PTY fallback never runs, regardless of rollout-file staleness

#### Scenario: Enabling takes effect without a restart

- **WHEN** an operator sets `codex_pty_fallback_enabled = true` in `config.toml` while the daemon is already running
- **THEN** the very next probe tick honors it, with no daemon restart required

#### Scenario: Disabling takes effect without a restart

- **WHEN** an operator removes or sets `codex_pty_fallback_enabled = false` while the daemon is already running
- **THEN** the very next probe tick stops spawning the costed fallback

### Requirement: Hera coordinator and sub-coordinator spawn are excluded from tier-list routing

The tier list (and the general task-backend resolution precedence it participates in — see `agent-execution`'s "Backend resolution precedence") SHALL NOT determine a hera coordinator's or sub-coordinator's backend. See `hera-coordination`'s "Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend" requirement for the full guarantee and its scenarios; this requirement exists so a reader of this capability's spec alone learns of the exclusion without needing to cross-reference `hera-coordination`.

#### Scenario: A coordinator spawn never resolves via the tier list's own choice when it is not Claude-capable

- **WHEN** a hera coordinator is spawned with no explicit backend and the configured tier list would otherwise resolve to a non-Claude-capable backend
- **THEN** the coordinator does not receive that backend — see `hera-coordination` for what it receives instead
