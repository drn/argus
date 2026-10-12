## ADDED Requirements

### Requirement: Backend listing carries family metadata

`GET /api/backends` SHALL include, per backend, its family `kind` (`claude`, `codex`, `pi`, `opencode`, `antigravity`, or empty for custom), a `label` (the family's human label when the backend is named after its family, otherwise the backend's own name so two instances of one family stay distinguishable), the prompt `skill_prefix` (`$` for codex, `/` otherwise), and `session_switcher` (whether the Claude session endpoints accept it). The create and update responses SHALL carry the same fields for the stored backend. Clients SHALL use these fields instead of hardcoding backend names.

#### Scenario: Second instance keeps its own label
- **WHEN** a backend named `claude-haiku` runs the `claude` command
- **THEN** its kind is `claude` and its label is `claude-haiku`

#### Scenario: Antigravity listed with metadata
- **WHEN** the backend roster contains `antigravity` with command `agy`
- **THEN** its entry has kind `antigravity`, label `Antigravity`, skill_prefix `/`, and session_switcher false
