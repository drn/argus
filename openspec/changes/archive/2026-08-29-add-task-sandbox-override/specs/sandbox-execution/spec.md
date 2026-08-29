## ADDED Requirements

### Requirement: Per-task sandbox override resolution

The system SHALL support an optional per-task sandbox override with three
states: unset (inherit), force-enabled, and force-disabled. When resolving the
effective sandbox config for a task, the system SHALL apply, in order: the
global setting, then the task's project override (if set), then the task's own
override (if set) — a set task override SHALL win over both the project and
global settings. An unset task override SHALL leave resolution exactly as it
was before this requirement existed (global, then project).

#### Scenario: Task override forces sandboxing on

- **WHEN** a task has a force-enabled sandbox override and its project has sandboxing disabled
- **THEN** the resolved sandbox config is enabled for that task

#### Scenario: Task override forces sandboxing off

- **WHEN** a task has a force-disabled sandbox override and both its project and the global setting have sandboxing enabled
- **THEN** the resolved sandbox config is disabled for that task

#### Scenario: Unset task override inherits the existing precedence

- **WHEN** a task has no sandbox override
- **THEN** the resolved sandbox config is exactly the project override if set, else the global setting, unchanged from prior behavior

#### Scenario: Override is baked in at creation time, not re-derived

- **WHEN** a task is created with a sandbox override
- **THEN** the task's persisted `Sandboxed` state reflects that override as resolved at creation time, and does not change if the global or project setting later changes
