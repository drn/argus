## ADDED Requirements

### Requirement: Project first UI preference defaults off and persists

The UI configuration SHALL provide `project_first`, default false, stored as `ui.project_first` in the existing configuration store and loaded by the normal configuration-loading path. Explicit true and false values SHALL survive closing and reopening the TUI. The preference SHALL use existing UI configuration precedence conventions and SHALL NOT change task creation APIs or task persistence schemas.

#### Scenario: Setting absent

- **WHEN** no project-first preference is configured
- **THEN** the effective value is false

#### Scenario: Persist and reload either state

- **WHEN** Settings saves true or false and configuration is loaded again
- **THEN** the effective project-first value matches the saved value under existing configuration precedence
