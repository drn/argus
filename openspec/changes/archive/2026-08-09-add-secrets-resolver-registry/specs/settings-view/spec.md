## ADDED Requirements

### Requirement: Secrets bootstrap status row in System category

The System category SHALL include a row surfacing the same RESOLVED / NOT
RESOLVED / NOT CONFIGURED tri-state for the `[secrets.op]` bootstrap source
(see `secrets-resolution`) that `argus doctor` reports, computed the same
way (one resolve-and-discard of `bootstrap_source`).

#### Scenario: Row reflects resolved status

- **WHEN** the `[secrets.op]` bootstrap source is configured and resolves
  successfully
- **THEN** the System category's secrets row shows RESOLVED

#### Scenario: Row reflects not-resolved status

- **WHEN** the `[secrets.op]` bootstrap source is configured but fails to
  resolve
- **THEN** the System category's secrets row shows NOT RESOLVED

#### Scenario: Row reflects not-configured status

- **WHEN** `[secrets]` is absent or `[secrets.op].bootstrap_source` is
  empty
- **THEN** the System category's secrets row shows NOT CONFIGURED
