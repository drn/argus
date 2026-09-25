## MODIFIED Requirements

### Requirement: Default backend selection persists

Pressing `d` on a non-default backend row SHALL mark that backend as the default and persist it under the `defaults.backend` config key — the same key the config-loading path reads — so the change is reflected the next time config is read (including on restart and when resolving the default for a new task); pressing `d` on the already-default backend SHALL be a no-op.

#### Scenario: Setting a new default backend

- **WHEN** the cursor is on a backend that is not the current default and the user presses `d`
- **THEN** the default backend becomes that backend and is written to the `defaults.backend` config key

#### Scenario: Pressing default on the current default

- **WHEN** the cursor is on the backend already marked default and the user presses `d`
- **THEN** nothing changes

#### Scenario: Default backend selection survives a config reload

- **WHEN** the default backend has been changed via the Settings view
- **THEN** re-reading config (e.g. on restart, or via `db.Config()`) SHALL resolve `Defaults.Backend` to the newly selected backend
