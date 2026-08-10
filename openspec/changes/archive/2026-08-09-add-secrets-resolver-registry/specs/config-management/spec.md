## ADDED Requirements

### Requirement: Secrets resolver configuration block

The system SHALL provide an optional `[secrets]` configuration block (with a
nested `[secrets.op]` table for 1Password bootstrap parameters) in
`config.toml`, consumed by the secrets-resolution registry (see
`secrets-resolution`). `[secrets.op]` SHALL carry a `bootstrap_source` string
(any scheme the registry supports, e.g. `keychain://...` or `env://...`) and
a `bootstrap_target` string naming the environment variable the resolved
bootstrap credential is set to in the `op` subprocess's own environment
(e.g. `OP_SERVICE_ACCOUNT_TOKEN`). When `[secrets]` is absent entirely, the
system SHALL behave identically to an instance with no `[secrets]` block
configured today — a pure no-op that does not change how any existing
bare-string `EnvVars` mapping resolves.

#### Scenario: Absent secrets block is a no-op

- **WHEN** a `config.toml` has no `[secrets]` block
- **THEN** the effective configuration behaves exactly as before this
  change: bare-string and `env://` sources resolve against the process
  environment, and no `op://` source can resolve

#### Scenario: Configured op bootstrap parameters

- **WHEN** `config.toml` sets `[secrets.op]` with
  `bootstrap_source = "keychain://op-service-account-claude"` and
  `bootstrap_target = "OP_SERVICE_ACCOUNT_TOKEN"`
- **THEN** the op resolver uses that Keychain descriptor to obtain a
  bootstrap credential and sets it under that target name in the `op read`
  subprocess's environment

#### Scenario: bootstrap_source accepts any supported scheme

- **WHEN** `[secrets.op].bootstrap_source` is set to an `env://`-prefixed
  descriptor instead of a `keychain://` one
- **THEN** the op resolver's bootstrap resolve dispatches to the `env`
  resolver, with no special-cased behavior for which scheme is used
