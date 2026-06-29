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
- **THEN** it SHALL include backend entries for `claude`, `codex`, and `pi`, each with a command template

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

A backend configuration entry MAY carry an optional `models` list naming the model identifiers offered for that backend in the new-task model selector. When a backend's `models` list is non-empty it SHALL override the built-in curated list for that backend; when it is empty or absent the built-in curated list (keyed on the backend command) SHALL apply. The built-in defaults SHALL stay configuration-free: a fresh, unconfigured instance SHALL still offer per-backend model options for recognized backends (Claude, Codex) without any `models` entry.

#### Scenario: Configured models override the built-in list

- **WHEN** a backend entry declares a non-empty `models` list
- **THEN** the new-task model selector for that backend offers exactly those models (plus the always-present `default` and `custom…` options)

#### Scenario: Absent models falls back to built-ins

- **WHEN** a backend entry omits `models` and its command is a recognized built-in backend
- **THEN** the new-task model selector offers that backend's built-in curated model list

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

