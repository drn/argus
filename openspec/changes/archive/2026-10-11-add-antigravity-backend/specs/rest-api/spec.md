## ADDED Requirements

### Requirement: Backend listing carries family metadata

`GET /api/backends` SHALL include, per backend, its family `kind` (`claude`, `codex`, `pi`, `opencode`, `antigravity`, or empty for custom), a human `label`, the prompt `skill_prefix` (`$` for codex, `/` otherwise), and `session_switcher` (whether the Claude session endpoints accept it). Clients SHALL use these fields instead of hardcoding backend names.

#### Scenario: Antigravity listed with metadata
- **WHEN** the backend roster contains `antigravity` with command `agy`
- **THEN** its entry has kind `antigravity`, label `Antigravity`, skill_prefix `/`, and session_switcher false
