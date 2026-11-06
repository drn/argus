## ADDED Requirements

### Requirement: Persistent task rail preference

Settings → Appearance SHALL expose a boolean “Persistent task rail” row backed by `ui.tasks_rail`. The preference SHALL default to false, support `[ui] tasks_rail = true` in initial TOML config import, persist through the existing config store, and apply without a restart-required marker or TUI restart.

#### Scenario: First run is off

- **WHEN** no explicit preference exists
- **THEN** the toggle is off and Tasks uses its existing layout

#### Scenario: Toggle persists and applies live

- **WHEN** the user enables the Appearance toggle and returns to Tasks
- **THEN** the rail layout appears immediately and remains enabled after relaunch

#### Scenario: Initial TOML preference is imported

- **WHEN** initial configuration contains `[ui]` with `tasks_rail = true`
- **THEN** the stored preference and Appearance row are enabled
