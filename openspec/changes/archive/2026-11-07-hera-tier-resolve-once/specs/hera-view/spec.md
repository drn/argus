## ADDED Requirements

### Requirement: The tier readout resolves once per model build

The system SHALL compute the plan view's per-role diligence-tier readout (applied model, applied effort, profile warning, context percentage) using one settings snapshot per model build, and SHALL load and validate any given diligence profile at most once per build for roles that share its inputs. No readout state SHALL persist across builds: every build SHALL re-read settings, project bindings and profile files. The resulting readout for each role SHALL be identical to resolving that role independently.

#### Scenario: Settings read once per build

- **WHEN** a model build annotates 200 roles
- **THEN** the application settings are read once for that build, not once per role

#### Scenario: Shared profile decoded once per build

- **WHEN** many roles in one project are annotated in one build and none has an in-repo profile directory
- **THEN** the project's profile file is decoded once for that build

#### Scenario: Output matches independent resolution

- **WHEN** a build annotates roles with differing projects, archetypes, task model overrides or in-repo profile directories
- **THEN** each role's applied model, effort and warning equal the values resolving that role alone would produce

#### Scenario: Edits appear on the next build

- **WHEN** a project's bound profile or a profile file is edited between two builds
- **THEN** the second build's readout reflects the edit without restarting the TUI
