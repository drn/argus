## ADDED Requirements

### Requirement: Backend routing tier list schema

The system SHALL support a `[backend_routing]` config.toml table containing an array of `[[backend_routing.tier]]` entries, each with a `backend` (string, must name an entry in `[backends]`), a `probe` (string, one of `claude_usage`, `codex_usage`, `none`), and a `threshold_pct` (integer, required and meaningful only for capped probe kinds). Absence of the `[backend_routing]` table, or an empty tier list, SHALL leave existing single-default-backend behavior fully unchanged.

#### Scenario: Table absent leaves behavior unchanged

- **WHEN** `config.toml` has no `[backend_routing]` table
- **THEN** backend resolution behaves exactly as it did before this change

#### Scenario: Tier entry validated against the backend roster

- **WHEN** a tier's `backend` field does not match any entry in `[backends]`
- **THEN** the tier is treated as misconfigured per the backend-tier-routing capability's skip behavior, not as a config load error

### Requirement: Backend routing tier list storage precedence

The system SHALL treat a `config.toml`-defined tier list as authoritative over any tier list persisted through the Settings UI, per the config.toml-over-DB precedence already established for other configuration sources (`DefaultConfig() < DB < config.toml`).

#### Scenario: config.toml present disables DB-sourced tier editing

- **WHEN** `config.toml` defines a non-empty tier list
- **THEN** the DB-persisted tier list, if any, is not consulted by the resolver
