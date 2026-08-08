## ADDED Requirements

### Requirement: Secrets bootstrap diagnostic

`argus doctor` SHALL additionally report the RESOLVED / NOT RESOLVED / NOT
CONFIGURED tri-state for the `[secrets.op]` bootstrap source (see
`secrets-resolution`'s "op bootstrap resolution status tri-state"), doing
one resolve-and-discard of `bootstrap_source` at check time. This check
SHALL be independent of the binary-coherence table and verdict and of the
Stop-hook and diligence-profile-library diagnostics: it is printed as its
own section and SHALL NOT alter `argus doctor`'s exit-code contract, which
remains governed solely by the binary-coherence verdict.

#### Scenario: Bootstrap resolves

- **WHEN** `[secrets.op].bootstrap_source` is configured and resolves
  successfully
- **THEN** `argus doctor` reports the secrets bootstrap status as RESOLVED

#### Scenario: Bootstrap configured but failing

- **WHEN** `[secrets.op].bootstrap_source` is configured but fails to
  resolve (e.g. a renamed Keychain item, or 1Password signed out)
- **THEN** `argus doctor` reports the secrets bootstrap status as NOT
  RESOLVED

#### Scenario: Secrets not configured

- **WHEN** `[secrets]` or `[secrets.op].bootstrap_source` is absent from
  configuration
- **THEN** `argus doctor` reports the secrets bootstrap status as NOT
  CONFIGURED, distinctly from NOT RESOLVED

#### Scenario: Check does not change the exit-code contract

- **WHEN** the secrets bootstrap status is NOT RESOLVED but the
  binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero
