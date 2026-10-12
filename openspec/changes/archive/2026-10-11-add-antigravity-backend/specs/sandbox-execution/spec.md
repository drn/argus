## MODIFIED Requirements

### Requirement: Backend auth and session persistence writes

The generated profile SHALL grant the narrow write paths each supported agent backend needs to persist auth tokens and session state, without broadening to all of `$HOME`. This covers `~/.claude.json` (including atomic-write sibling files), `~/.claude/`, `~/.codex/`, `~/.local/share/argus/codex-home/`, `~/.pi/`, and agy's `~/.gemini/antigravity-cli/`, `~/.gemini/config/`, and `~/.gemini/jetski/` (never all of `~/.gemini`, whose root holds Gemini CLI credentials). The grants SHALL be declared on each backend family in the registry. A family whose grants would let another backend's agent tamper with configuration an unsandboxed CLI later trusts (agy's `~/.gemini/...`) SHALL have them emitted only into its own launches' profiles, never into other backends' sandboxed tasks. A family whose grant directories sit under a parent the sandbox cannot write (agy's `~/.gemini/...`) SHALL opt in to having those directories created unsandboxed before a sandboxed launch, since a sandboxed process cannot create a missing parent of its own grant; other families' directories SHALL NOT be created. Because `~/.gemini/config/` is writable by any sandboxed agy session, such a session can alter agy's global MCP and skill registrations for later agy sessions; this is an accepted consequence of agy having no per-launch configuration. Unrelated `$HOME`-rooted paths SHALL remain denied.

#### Scenario: Claude auth file and atomic-write siblings are writable

- **WHEN** a sandboxed command writes `~/.claude.json` or an atomic-write sibling such as `~/.claude.json.tmp.NNN`, `~/.claude.json.backup`, or `~/.claude.json.lock`
- **THEN** each write succeeds, preserving OAuth token persistence

#### Scenario: Codex and Pi session state are writable

- **WHEN** a sandboxed command writes Codex state under `~/.codex/` (auth, session DB, sessions) or Pi session files under `~/.pi/`
- **THEN** the writes succeed so the backends can persist auth and resume sessions

#### Scenario: Antigravity state is writable

- **WHEN** a sandboxed agy command writes under `~/.gemini/antigravity-cli/` or `~/.gemini/config/` on a machine where `~/.gemini` did not exist before the launch
- **THEN** the write succeeds

#### Scenario: Gemini CLI credentials stay protected

- **WHEN** a sandboxed command writes a file directly under `~/.gemini/`
- **THEN** the write is denied

#### Scenario: Argus-only Codex home is writable

- **WHEN** an Argus-launched Codex command writes under `~/.local/share/argus/codex-home/`
- **THEN** the write succeeds without granting access to all of `~/.local/share/`

#### Scenario: unrelated HOME path stays denied

- **WHEN** a sandboxed command writes an unrelated `$HOME`-rooted path such as `~/.not-claude-related`
- **THEN** the write is denied, confirming the backend rules are not over-broad
