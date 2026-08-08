# Configuration Management

## Purpose

Configuration Management defines the in-memory configuration model for Argus and the sensible defaults the application falls back to when no explicit settings exist. It supplies the default backend roster, keybindings, UI preferences, network ports, and sandbox settings, and provides helpers for resolving optional settings and discovering local Obsidian (knowledge base) vaults. It is the single source of truth for what a fresh, unconfigured Argus instance behaves like.
## Requirements
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

### Requirement: Default keybindings

The system SHALL provide a default set of single-key bindings for core actions.

#### Scenario: Core action keys

- **WHEN** default keybindings are produced
- **THEN** the bindings SHALL be: new=`n`, attach=`enter`, status=`s`, delete=`d`, quit=`q`, help=`?`, filter=`/`, prompt=`p`, worktree=`w`

### Requirement: Worktree cleanup default resolution

The system SHALL resolve whether worktrees are auto-removed on task delete from an optional setting, defaulting to enabled when the setting is unset.

#### Scenario: Unset defaults to cleanup enabled

- **WHEN** the cleanup-worktrees preference is unset
- **THEN** worktree cleanup SHALL be reported as enabled

#### Scenario: Explicit enable

- **WHEN** the cleanup-worktrees preference is explicitly set to true
- **THEN** worktree cleanup SHALL be reported as enabled

#### Scenario: Explicit disable

- **WHEN** the cleanup-worktrees preference is explicitly set to false
- **THEN** worktree cleanup SHALL be reported as disabled

### Requirement: Default Metis vault path

The system SHALL compute a default knowledge base vault path under the user's iCloud-synced Obsidian directory.

#### Scenario: Path derived from home directory

- **WHEN** the default Metis vault path is requested
- **THEN** it SHALL be the user's home directory joined with the iCloud Obsidian Documents base and the `Metis` vault name

### Requirement: Obsidian vault discovery

The system SHALL discover Obsidian vaults within a base directory, identifying a vault by the presence of a `.obsidian` subdirectory, and return sorted absolute paths.

#### Scenario: Discovers a child vault

- **WHEN** a base directory contains a child directory holding a `.obsidian` subdirectory
- **THEN** that child directory's path SHALL be returned

#### Scenario: Discovers the base directory as a vault

- **WHEN** the base directory itself holds a `.obsidian` subdirectory
- **THEN** the base directory's path SHALL be returned, in addition to any qualifying children

#### Scenario: Skips non-vault and hidden directories

- **WHEN** the base contains directories without a `.obsidian` subdirectory or directories whose names begin with a dot, and plain files
- **THEN** those entries SHALL be excluded from the results

#### Scenario: Results are sorted

- **WHEN** multiple vaults are discovered
- **THEN** the returned paths SHALL be in ascending sorted order

#### Scenario: Missing or empty base yields nil

- **WHEN** the base directory does not exist or contains no vaults
- **THEN** the result SHALL be nil

### Requirement: iCloud vault discovery

The system SHALL discover Obsidian vaults specifically under the user's iCloud-synced Obsidian base directory.

#### Scenario: iCloud base absent

- **WHEN** the user's iCloud Obsidian base directory does not exist
- **THEN** the result SHALL be nil

#### Scenario: Vaults present under iCloud base

- **WHEN** the iCloud Obsidian base directory contains qualifying vaults
- **THEN** their absolute paths SHALL be returned in sorted order

### Requirement: Per-backend model option list

A backend configuration entry MAY carry an optional `models` list naming the model identifiers offered for that backend in the new-task model selector. When a backend's `models` list is non-empty it SHALL override the built-in curated list for that backend; when it is empty or absent the built-in curated list (keyed on the backend command) SHALL apply. The built-in defaults SHALL stay configuration-free: a fresh, unconfigured instance SHALL still offer per-backend model options for recognized backends (Claude, Codex) without any `models` entry. The Claude backend's built-in curated list SHALL be the stable `claude` CLI aliases `opus`, `sonnet`, `haiku`, and `fable`. For opencode there SHALL be no built-in curated list — its model space is `provider/model` and depends on the user's authenticated providers — so an unconfigured opencode backend SHALL offer only the always-present `default` and `custom…` options, while a `models` entry still overrides as for any backend.

#### Scenario: Configured models override the built-in list

- **WHEN** a backend entry declares a non-empty `models` list
- **THEN** the new-task model selector for that backend offers exactly those models (plus the always-present `default` and `custom…` options)

#### Scenario: Absent models falls back to built-ins

- **WHEN** a backend entry omits `models` and its command is a recognized built-in backend
- **THEN** the new-task model selector offers that backend's built-in curated model list

#### Scenario: opencode offers custom-only without configuration

- **WHEN** the opencode backend has no `models` entry
- **THEN** the new-task model selector offers only the `default` and `custom…` options (no curated list), so any `provider/model` is reachable by typing

### Requirement: Project profile binding

Project configuration SHALL support an optional `profile` field naming the diligence profile bound to
that project, storing the profile **name only** (never the profile body). When a project's `profile` is
empty or absent, the project SHALL be treated as bound to the `default` profile for resolution purposes.

#### Scenario: Project carries a profile name

- **WHEN** a project entry declares `profile = "customer_grade"`
- **THEN** the loaded project exposes `customer_grade` as its bound profile name

#### Scenario: Absent profile resolves default

- **WHEN** a project entry omits `profile`
- **THEN** the project is treated as bound to the `default` profile

### Requirement: Coordinator context budget configuration

The system SHALL provide a `coordinator_context_budget` integer field on `HeraConfig` (`config.toml` key `hera.coordinator_context_budget`), naming the token count at or above which the context-budget Stop hook (see `coordinator-context-management`) begins nudging a coordinator to recycle. When absent from config, the system SHALL default it to `300000`.

#### Scenario: Default budget applies when unset

- **WHEN** a project's config.toml has no `hera.coordinator_context_budget` key
- **THEN** the effective budget is `300000`

#### Scenario: Explicit budget overrides the default

- **WHEN** a project's config.toml sets `hera.coordinator_context_budget = 350000`
- **THEN** the effective budget for that project is `350000`

### Requirement: Worker context window configuration

The system SHALL provide a `worker_context_window` integer field on `HeraConfig` (`config.toml` key
`hera.worker_context_window`), naming the reference context window size the Hera rail's
worker/freelance context-pressure indicator (see `hera-view`) divides a role's `ContextSize` by to
compute its percentage. This is deliberately a separate field from `coordinator_context_budget` — a
coordinator recycle-nudge policy threshold, not a context window size — since a worker runs with a
much larger real context window. When absent from config, the system SHALL default it to `1000000`.

#### Scenario: Default worker context window applies when unset

- **WHEN** a project's config.toml has no `hera.worker_context_window` key
- **THEN** the effective worker context window is `1000000`

#### Scenario: Explicit worker context window overrides the default

- **WHEN** a project's config.toml sets `hera.worker_context_window = 500000`
- **THEN** the effective worker context window for that project is `500000`

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

