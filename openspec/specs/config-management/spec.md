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
