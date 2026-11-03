## ADDED Requirements

### Requirement: Project first preference is editable in Appearance

TUI Settings SHALL expose a “Project first” boolean row in Appearance, explaining that it asks for a project before new tasks and coordinators when multiple projects exist. Enter or Right SHALL toggle the value using existing Settings conventions and persist it under `ui.project_first`. Left SHALL retain its existing return-to-rail behavior. Successful changes SHALL affect subsequently opened popups immediately without restart, and persistence failures SHALL be visible and logged.

#### Scenario: Enable the preference

- **WHEN** the user activates the Project first row while it is off
- **THEN** it is enabled and persisted, the row reflects the new value, and the next eligible popup starts with the project question

#### Scenario: Disable the preference

- **WHEN** the user activates the Project first row while it is on
- **THEN** it is disabled and persisted, and the next popup opens the existing full form directly

#### Scenario: Leave the settings pane

- **WHEN** Project first is selected and the user presses Left
- **THEN** focus returns to the Settings rail without changing the preference
