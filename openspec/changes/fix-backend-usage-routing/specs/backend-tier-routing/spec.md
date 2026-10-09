## ADDED Requirements

### Requirement: Routing strategy is selectable between ordered and headroom

The system SHALL read a `[backend_routing].strategy` config.toml setting whose value is `ordered` or `headroom`, defaulting to `ordered` when absent. An unrecognized value SHALL be logged and treated as `ordered`. In `ordered` mode the resolver SHALL behave exactly as the "Ordered tier list drives default backend resolution" requirement describes. The setting SHALL be read live on every resolution and SHALL NOT have a DB/Settings-UI surface in this version.

#### Scenario: Absent strategy keeps ordered behavior

- **WHEN** `[backend_routing]` has no `strategy` key
- **THEN** the resolver returns the first available tier in list order, identical to the ordered requirement

#### Scenario: Unrecognized strategy is treated as ordered

- **WHEN** `strategy` is set to a value other than `ordered` or `headroom`
- **THEN** the resolver behaves as `ordered` and logs the unrecognized value

### Requirement: Headroom strategy picks the backend with the most room under its threshold

In `headroom` mode the resolver SHALL compute, for every valid capped tier that has a fresh weekly reading below its threshold, a headroom score of `threshold_pct − used percentage`, and SHALL return the tier with the highest score, breaking ties by list order. Tiers at or above their threshold SHALL NOT be selected. Only when no such known candidate exists SHALL the resolver consider tiers without a usable reading — capped tiers whose reading is unknown or stale, and `none`-probe tiers — returning the first of those in list order. If neither set has a candidate, the resolver SHALL return empty.

#### Scenario: Most headroom wins

- **WHEN** strategy is `headroom`, Claude is at 40% with threshold 70 (headroom 30) and Codex is at 20% with threshold 80 (headroom 60)
- **THEN** the resolver returns Codex

#### Scenario: Ties break by list order

- **WHEN** two known tiers have equal headroom
- **THEN** the resolver returns the one listed first

#### Scenario: Known reading beats unknown reading

- **WHEN** strategy is `headroom`, Claude has a fresh reading under threshold and Codex has no reading
- **THEN** the resolver returns Claude

#### Scenario: All unknown falls back to list order

- **WHEN** strategy is `headroom` and no tier has a fresh reading
- **THEN** the resolver returns the first valid tier in list order

#### Scenario: Over-threshold tier is excluded

- **WHEN** strategy is `headroom`, Claude is at or above its threshold and Codex has no reading
- **THEN** the resolver returns Codex

#### Scenario: Every tier over threshold

- **WHEN** strategy is `headroom` and every tier is capped with a fresh reading at or above its threshold
- **THEN** the resolver returns empty and the caller falls back to the default-backend precedence

## MODIFIED Requirements

### Requirement: Claude usage probe (existing behavior, reused)

The system SHALL determine Claude usage via the headless `/usage` PTY probe and in-memory cache defined by `usage-budget-routing`, fail-open on any probe or parse error, and cache the weekly percentage + reset timestamp for synchronous, non-blocking reads by the tier resolver. Only the weekly ("current week, all models") window SHALL be used for routing.

#### Scenario: Cache read never blocks resolution

- **WHEN** the tier resolver reads the Claude probe's cached usage percentage
- **THEN** it never triggers a live probe synchronously

### Requirement: Codex usage probe prefers a free local-file read, falls back to a costed live probe

The system SHALL determine Codex usage primarily by reading the `rate_limits` object from the Codex CLI rollout log files (`~/.codex/sessions/**/rollout-*.jsonl`), without spawning any subprocess, walking at most the 10 most recently modified rollouts newest first and using the first that yields a usable, unexpired reading. The system SHALL select the weekly window as whichever of `primary` or `secondary` reports `window_minutes` equal to 10080, and SHALL ignore any other window. When the latest `rate_limits` record has a non-null `rate_limit_reached_type` and no weekly window, the system SHALL record a 100% reading held for 24 hours from that rollout file's last-modified time (re-reading the same rollout SHALL NOT extend the hold). Otherwise the reading SHALL be treated as fresh until its weekly `resets_at`, regardless of the rollout file's age, and as unknown once `resets_at` has passed. No reading SHALL stay valid longer than 8 days past its anchor (the rollout file's last-modified time, or the probe time for a PTY `/status` reading). When the latest rollout yields no usable reading, the system SHALL fall back to a headless `codex` PTY probe that sends a minimal message and reads the weekly row of the rendered `/status` output (ignoring the 5-hour row), on the same background cadence as the Claude probe, ONLY when that fallback has been explicitly opted into (see "Costed Codex PTY fallback is opt-in and disabled by default" below). When the fallback is not opted into, a missing reading SHALL leave the cached reading stale/unknown rather than spending Codex quota. Both paths SHALL fail open: any read, parse, or subprocess error leaves the previous cached value in place (or unknown, if none exists) and logs the outcome to the daemon log without erroring the caller.

#### Scenario: Weekly window is found by duration, not slot

- **WHEN** the latest rollout record has `primary` with `window_minutes` 10080 and `secondary` null
- **THEN** the probe records `primary.used_percent` as the weekly reading

#### Scenario: Non-weekly windows are ignored

- **WHEN** the latest record has a 300-minute window at 95% and a 10080-minute window at 40%
- **THEN** the probe records 40%

#### Scenario: Depleted account reads as full

- **WHEN** the latest record has `rate_limit_reached_type` set and both windows null
- **THEN** the probe records a 100% reading

#### Scenario: Old rollout stays valid until its reset

- **WHEN** the latest rollout file is three days old and its weekly `resets_at` is still in the future
- **THEN** the probe records that reading as fresh without spawning a `codex` subprocess

#### Scenario: Reading past its reset is unknown

- **WHEN** the latest rollout's weekly `resets_at` is in the past
- **THEN** the cached reading is treated as unknown

#### Scenario: Short newest rollout does not hide an older valid reading

- **WHEN** the newest rollout has no `rate_limits` record and an older rollout among the 10 newest has an unexpired weekly reading
- **THEN** the probe records the older rollout's reading

#### Scenario: Far-future reset is capped

- **WHEN** a rollout's weekly `resets_at` lies more than 8 days after the rollout file's last-modified time
- **THEN** the reading becomes unknown 8 days after that last-modified time

#### Scenario: PTY fallback reads only the weekly row

- **WHEN** the opted-in `/status` output shows a 5-hour row at 95% and a weekly row at 40%
- **THEN** the probe records 40% with the weekly row's own reset

#### Scenario: No usable rollout falls back to the live PTY probe when opted in

- **WHEN** no rollout yields a usable reading AND the costed PTY fallback is opted into
- **THEN** the probe spawns a headless `codex` session, sends a minimal message, and parses the rendered `/status` output

#### Scenario: No usable rollout leaves the cache stale when the fallback is not opted into

- **WHEN** no rollout yields a usable reading AND the costed PTY fallback has NOT been opted into (the default)
- **THEN** the probe does not spawn a `codex` subprocess, the cache is left exactly as it was (stale or unknown), and the skip is logged to the daemon log

#### Scenario: Live probe fails

- **WHEN** the fallback `codex` PTY probe's subprocess errors, times out, or its output does not parse
- **THEN** the cache is left unchanged (or unknown) and the failure is logged, with no error propagated to any caller
