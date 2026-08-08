## ADDED Requirements

### Requirement: Default secret-resolver configuration

The system SHALL provide a `[secrets]` configuration table, config.toml-only
(no database table, no Settings-menu surface), that selects and configures the
secret-resolver mode consulted when resolving a backend's credential
environment mapping. A default configuration SHALL set the resolver mode to
`"env"` and SHALL provide no default 1Password object-reference template,
command override, or timeout — every field of the `op` sub-table SHALL be
absent/zero-valued until an operator explicitly sets it, so that no
installation is defaulted toward assuming a particular 1Password vault,
account, or item exists.

#### Scenario: Default configuration selects the environment resolver

- **WHEN** a default configuration is produced
- **THEN** the secret-resolver mode SHALL be `"env"`
- **AND** the `op` sub-table's reference template SHALL be empty

#### Scenario: Absent `[secrets]` table parses as the default

- **WHEN** a `config.toml` file with no `[secrets]` table is loaded
- **THEN** the resulting configuration SHALL be equivalent to the default
  secret-resolver configuration

### Requirement: Secret-resolver mode validation is fail-open

The system SHALL NOT hard-fail configuration loading or agent spawning on an
invalid or incomplete `[secrets]` configuration. An unrecognized resolver mode,
an empty reference template under `"op"` mode, or an unresolvable `op` command
SHALL each fall back to the environment resolver rather than blocking
configuration load or command construction.

#### Scenario: Invalid resolver mode does not block configuration load

- **WHEN** `config.toml` sets `[secrets] resolver` to a value other than
  `"env"` or `"op"`
- **THEN** configuration loading succeeds and the invalid value is carried
  through unchanged for the command builder to fall open on

#### Scenario: Incomplete `op` configuration does not block configuration load

- **WHEN** `config.toml` sets `[secrets] resolver = "op"` with an empty
  `[secrets.op] reference_template`
- **THEN** configuration loading succeeds; the fall-open behavior is applied
  by the command builder, not by configuration loading
