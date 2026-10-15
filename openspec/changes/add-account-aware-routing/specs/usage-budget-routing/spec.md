## ADDED Requirements

### Requirement: The Claude usage probe runs per distinct account identity

When the tier list contains Claude tiers on more than one account, the system SHALL run the Claude `/usage` probe once per distinct signed-in identity (see `backend-tier-routing`'s "Routing candidates are keyed by signed-in identity"), each run with `CLAUDE_CONFIG_DIR` set to that identity's account folder (unset for the `default` account, so `~/.claude` is used), in the same dedicated probe directory and with the same trust-dialog handling as the single-account probe. Readings SHALL be cached per identity, and each identity's probe SHALL fail open independently: one account's probe failure SHALL NOT affect another account's cached reading. With only `default`-account Claude tiers, the probe SHALL behave exactly as before.

#### Scenario: Each account is probed in its own folder

- **WHEN** Claude tiers exist for `default` and `personal`, signed into different logins
- **THEN** the daemon runs two `/usage` probes per tick — one with no `CLAUDE_CONFIG_DIR`, one with `CLAUDE_CONFIG_DIR` set to the `personal` folder — and caches a reading for each login

#### Scenario: Collapsed identities are probed once

- **WHEN** `default` and `personal` are signed into the same login
- **THEN** the daemon runs one `/usage` probe for that login per tick

#### Scenario: One account's probe failure does not affect the other

- **WHEN** the `personal` probe times out and the `default` probe succeeds
- **THEN** the `default` reading is updated and the `personal` reading keeps its previous value (or stays unknown)

#### Scenario: Single-account configuration is unchanged

- **WHEN** every Claude tier uses the `default` account
- **THEN** exactly one Claude `/usage` probe runs per tick, against `~/.claude`, as before
