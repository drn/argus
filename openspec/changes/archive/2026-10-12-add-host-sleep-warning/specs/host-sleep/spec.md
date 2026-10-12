# Host Sleep

## ADDED Requirements

### Requirement: Host sleep posture diagnosis

The system SHALL classify the host's sleep posture as `not_applicable`, `safe`, `held`, `at_risk`, or `unknown`. The classification SHALL be `not_applicable` when the remote API is disabled or the host is not macOS. Otherwise it SHALL be `unknown` when `pmset` cannot be read or parsed, `safe` when the AC-power `sleep` setting is `0`, `held` when AC sleep is non-zero and a `caffeinate` assertion exists on behalf of the daemon's PID, and `at_risk` in every other case. Only `at_risk` SHALL be presented as a warning.

#### Scenario: Remote API disabled is never a warning

- **WHEN** the remote API is disabled and AC `sleep` is `1`
- **THEN** the posture is `not_applicable`

#### Scenario: Idle-sleeping host with the API enabled warns

- **WHEN** the remote API is enabled, AC `sleep` is `1`, and no `caffeinate` assertion is bound to the daemon PID
- **THEN** the posture is `at_risk` with the permanent fix `sudo pmset -c sleep 0 displaysleep 10`

#### Scenario: A daemon-bound caffeinate is held, not at risk

- **WHEN** the remote API is enabled, AC `sleep` is `1`, and `pmset -g assertions` shows a caffeinate assertion on behalf of the daemon PID
- **THEN** the posture is `held`

#### Scenario: Unreadable power settings are reported distinctly

- **WHEN** `pmset` fails or its output has no AC `sleep` value
- **THEN** the posture is `unknown`, never assumed `safe` or `at_risk`

### Requirement: Host sleep surfaces

`argus doctor` SHALL print a Host sleep block that never affects its exit code and never opens the database for writing. `GET /api/system-metrics` SHALL include a `host_sleep` object. The TUI Settings Remote API pane, the web Settings tab, and the macOS System tab SHALL show a warning when and only when the posture is `at_risk`. Checks SHALL NOT block the UI thread or request handler on `pmset`.

#### Scenario: Web banner shows only when at risk

- **WHEN** `/api/system-metrics` returns `host_sleep.status = "at_risk"`
- **THEN** the web Settings tab shows the warning with the fix command; for any other status the banner stays hidden

#### Scenario: An older daemon without the field

- **WHEN** the macOS app decodes a `/api/system-metrics` response with no `host_sleep`
- **THEN** it decodes successfully and shows no banner
