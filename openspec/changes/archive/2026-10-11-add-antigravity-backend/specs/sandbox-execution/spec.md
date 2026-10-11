## MODIFIED Requirements

### Requirement: Backend auth and session persistence writes

The generated profile SHALL grant the narrow write paths each supported agent backend needs to persist auth tokens and session state, without broadening to all of `$HOME`. This covers `~/.claude.json` (including atomic-write sibling files), `~/.claude/`, `~/.codex/`, `~/.local/share/argus/codex-home/`, `~/.pi/`, and `~/.gemini/`. The grants SHALL be declared on each backend family in the registry. Unrelated `$HOME`-rooted paths SHALL remain denied.

#### Scenario: Claude auth file and atomic-write siblings are writable

- **WHEN** a sandboxed command writes `~/.claude.json` or an atomic-write sibling such as `~/.claude.json.tmp.NNN`, `~/.claude.json.backup`, or `~/.claude.json.lock`
- **THEN** each write succeeds, preserving OAuth token persistence

#### Scenario: Codex and Pi session state are writable

- **WHEN** a sandboxed command writes Codex state under `~/.codex/` (auth, session DB, sessions) or Pi session files under `~/.pi/`
- **THEN** the writes succeed so the backends can persist auth and resume sessions

#### Scenario: Antigravity state is writable

- **WHEN** a sandboxed command writes under `~/.gemini/` (agy logs, conversation database, sign-in state)
- **THEN** the write succeeds

#### Scenario: Argus-only Codex home is writable

- **WHEN** an Argus-launched Codex command writes under `~/.local/share/argus/codex-home/`
- **THEN** the write succeeds without granting access to all of `~/.local/share/`

#### Scenario: unrelated HOME path stays denied

- **WHEN** a sandboxed command writes an unrelated `$HOME`-rooted path such as `~/.not-claude-related`
- **THEN** the write is denied, confirming the backend rules are not over-broad
