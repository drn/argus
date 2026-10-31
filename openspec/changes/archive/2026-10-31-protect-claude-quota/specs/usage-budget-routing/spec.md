## MODIFIED Requirements

### Requirement: Weekly usage percentage is probed and cached, never checked live

The system SHALL periodically sample Claude's five-hour session and seven-day all-models usage windows via a background probe and retain each parsed window's percentage, reset timestamp, and last-probed-at independently. Synchronous reads SHALL return the fuller still-valid window without launching a live probe. A stale window or one whose reset has passed SHALL be ignored independently; if neither window is usable, usage SHALL be unknown. Existing weekly-only output SHALL remain usable.

#### Scenario: Session pressure exceeds weekly pressure

- **WHEN** fresh session usage is 96% and weekly usage is 75%, with both resets in the future
- **THEN** the cached reading is 96%

#### Scenario: Session reset reveals the weekly window

- **WHEN** the 96% session window has reset but the 75% weekly window is still valid
- **THEN** the cached reading is 75% without requiring another probe

#### Scenario: Weekly window binds

- **WHEN** weekly usage exceeds session usage and both windows are valid
- **THEN** the cached reading is the weekly percentage

#### Scenario: Cache read never blocks a backend resolution

- **WHEN** routing reads usage
- **THEN** only cached state is read, and missing or stale state is unknown

### Requirement: Fresh bridge rate limits precede hidden probes

Usage routing SHALL prefer fresh session.measure readings from live Claude bridges on the requested account. For each of five_hour and seven_day, it SHALL use the freshest measurement with a valid percentage and future reset, then return the fuller valid window. The headless probe SHALL run only when no fresh applicable reading exists. Default and named accounts SHALL remain isolated.

At startup and every 30 minutes, the headless poller SHALL probe the default account and each distinct valid Claude tier account using its CLAUDE_CONFIG_DIR, with inherited auth overrides and parent-session markers stripped as for spawning. Caches SHALL be isolated per account. Each account's fresh bridge reading SHALL skip its hidden probe; errors SHALL leave that account's previous reading unchanged and fail open under existing staleness rules.

#### Scenario: Live account reading

- **WHEN** a live bridge on the requested account has a fresh session or weekly limit
- **THEN** routing uses the fuller valid percentage without launching a hidden Claude session

#### Scenario: Different account or stale reading

- **WHEN** only another account or stale measurements exist
- **THEN** they are ignored and only the requested account's fallback is available

#### Scenario: Independent bridge window validity

- **WHEN** a bridge supplies an expired session limit and a valid weekly limit
- **THEN** the weekly limit remains usable

#### Scenario: Idle personal account is measured before routing

- **WHEN** personal is configured in a Claude tier with no live bridge
- **THEN** the startup/ticker probe measures it with its own CLAUDE_CONFIG_DIR and caches its reading independently
