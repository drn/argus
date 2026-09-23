## ADDED Requirements

### Requirement: Backend tier list is viewable and editable in Settings

The system SHALL provide a Settings category presenting the configured backend-routing tier list in order, each row showing its backend name, probe kind, and threshold (when capped). When the active tier list is sourced from `config.toml`, the category SHALL render every row read-only with an indication that it is config.toml-defined, mirroring the existing config.toml-is-authoritative rendering used elsewhere in Settings. When no `config.toml` tier list is defined, the category SHALL be fully editable.

#### Scenario: config.toml-sourced list renders read-only

- **WHEN** `config.toml` defines the active tier list
- **THEN** the category's rows are displayed but cannot be added, removed, reordered, or edited from the TUI

#### Scenario: DB-sourced list is editable

- **WHEN** no `config.toml` tier list is defined
- **THEN** the category's rows can be added, removed, reordered, and edited

### Requirement: Tiers can be added, removed, and reordered

The system SHALL let the user add a new tier (choosing a backend from the configured roster, a probe kind, and a threshold when applicable), remove the selected tier, and move the selected tier up or down one position in the list, persisting the resulting order immediately.

#### Scenario: Adding a tier appends it and persists

- **WHEN** the user adds a new tier with a valid backend and probe kind
- **THEN** the tier is appended to the list and the change is persisted

#### Scenario: Removing a tier persists the shortened list

- **WHEN** the user removes the selected tier
- **THEN** the tier list is persisted without that entry, and later tiers retain their relative order

#### Scenario: Reordering persists the new order

- **WHEN** the user moves the selected tier up or down
- **THEN** the tier list is persisted in its new order

### Requirement: Tier probe kind and threshold are editable per row

The system SHALL let the user cycle a tier's probe kind among the registered kinds and edit its threshold percentage when the kind is capped; the threshold field SHALL be inert (not editable, not required) when the probe kind is `none`.

#### Scenario: Threshold hidden for an uncapped tier

- **WHEN** a tier's probe kind is `none`
- **THEN** no threshold value is editable or required for that row

#### Scenario: Threshold editable for a capped tier

- **WHEN** a tier's probe kind is `claude_usage` or `codex_usage`
- **THEN** the threshold percentage can be edited and is persisted with the tier
