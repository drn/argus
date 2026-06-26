## MODIFIED Requirements

### Requirement: Default configuration

The system SHALL provide a baseline configuration with sensible defaults so that an instance with no stored settings is fully usable.

#### Scenario: Default backend selection

- **WHEN** a default configuration is produced
- **THEN** the default backend SHALL be `claude`

#### Scenario: Built-in backends present

- **WHEN** a default configuration is produced
- **THEN** it SHALL include backend entries for `claude`, `codex`, `pi`, and `opencode`, each with a command template
- **AND** the `opencode` entry SHALL use the bare `opencode` command with `--prompt` as its prompt flag, leaving the user's opencode permission posture unmodified

#### Scenario: Projects map initialized

- **WHEN** a default configuration is produced
- **THEN** the projects collection SHALL be non-nil (an empty, ready-to-populate map) rather than nil

#### Scenario: Default UI preferences

- **WHEN** a default configuration is produced
- **THEN** the UI theme SHALL be `default`, elapsed-time display and icons SHALL both be enabled, and the spinner style SHALL be `progress`

#### Scenario: Default service ports

- **WHEN** a default configuration is produced
- **THEN** the knowledge base port SHALL default to `7742` and the REST API port SHALL default to `7743`

### Requirement: Per-backend model option list

A backend configuration entry MAY carry an optional `models` list naming the model identifiers offered for that backend in the new-task model selector. When a backend's `models` list is non-empty it SHALL override the built-in curated list for that backend; when it is empty or absent the built-in curated list (keyed on the backend command) SHALL apply. The built-in defaults SHALL stay configuration-free: a fresh, unconfigured instance SHALL still offer per-backend model options for recognized backends (Claude, Codex) without any `models` entry. For opencode there SHALL be no built-in curated list — its model space is `provider/model` and depends on the user's authenticated providers — so an unconfigured opencode backend SHALL offer only the always-present `default` and `custom…` options, while a `models` entry still overrides as for any backend.

#### Scenario: Configured models override the built-in list

- **WHEN** a backend entry declares a non-empty `models` list
- **THEN** the new-task model selector for that backend offers exactly those models (plus the always-present `default` and `custom…` options)

#### Scenario: Absent models falls back to built-ins

- **WHEN** a backend entry omits `models` and its command is a recognized built-in backend
- **THEN** the new-task model selector offers that backend's built-in curated model list

#### Scenario: opencode offers custom-only without configuration

- **WHEN** the opencode backend has no `models` entry
- **THEN** the new-task model selector offers only the `default` and `custom…` options (no curated list), so any `provider/model` is reachable by typing
