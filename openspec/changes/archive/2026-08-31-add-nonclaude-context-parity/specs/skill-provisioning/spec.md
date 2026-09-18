## ADDED Requirements

### Requirement: Codex vendor-scoped builtin skill materialization

The system SHALL materialize the same embedded builtin skill bodies used for the Claude-targeted `~/.argus/skills/.claude/skills/` workspace to Codex's own first-party installed-skills directory, `$CODEX_HOME/skills/<name>/SKILL.md` (respecting the `CODEX_HOME` environment variable when set, defaulting to `~/.codex/skills/`), so that Codex's native `SKILL.md` discovery finds argus's builtin skills without any per-backend integration code in Codex itself. This target SHALL be distinct from Codex's own bundled `$CODEX_HOME/skills/.system/` content and from the generic cross-tool `.agents/skills` convention, to bound the exposure of argus's builtin skills to Codex sessions specifically rather than any tool implementing the broader convention. Materialization SHALL be idempotent (rewrite a file only when its content differs from what is already on disk) and SHALL remove materialized skill directories under this target that no longer correspond to an embedded skill, mirroring the existing Claude-targeted materialization's behavior. This materialization SHALL be inert (no filesystem writes, no error) when running inside a Go test binary, mirroring the existing Claude-targeted materialization.

#### Scenario: Embedded skills materialized to the Codex-scoped path

- **WHEN** the Codex-scoped materialization runs
- **THEN** every embedded builtin skill's `SKILL.md` (and any accompanying files) appears under `$CODEX_HOME/skills/<name>/` (outside `.system/`), matching the embedded source content

#### Scenario: Stale skill directories removed

- **WHEN** the Codex-scoped materialization runs and a directory exists under `$CODEX_HOME/skills/` (outside `.system/`) whose name does not correspond to any currently-embedded skill
- **THEN** that directory is removed

#### Scenario: Materialization is inert during automated tests

- **WHEN** the Codex-scoped materialization runs inside a Go test binary
- **THEN** it performs no filesystem writes under `$CODEX_HOME/skills/` and returns no error

### Requirement: opencode skills-path config injection

The system SHALL ensure opencode's own configuration names Argus's managed skills workspace as an additional skill-discovery location, via opencode's documented `skills` config array, so that opencode's native skill discovery finds argus's builtin skills without relying on the generic cross-tool `.agents/skills` convention. This SHALL be implemented as an idempotent addition to the same configuration file the existing `mcp.argus` entry is injected into (`~/.config/opencode/opencode.json`): the entry SHALL be added only when not already present, and all unrelated keys — including other `skills` array entries a user or another tool has added — SHALL be preserved.

#### Scenario: Skills path entry added when absent

- **WHEN** injection runs against an opencode config whose `skills` array does not already name Argus's managed skills workspace
- **THEN** the workspace path is appended to the `skills` array without removing any existing entries

#### Scenario: Idempotent on repeat

- **WHEN** injection runs twice
- **THEN** the second run does not duplicate the entry or otherwise rewrite the file

#### Scenario: Unrelated config preserved

- **WHEN** the opencode config already contains other `skills` entries, an `mcp.argus` entry, and unrelated top-level keys
- **THEN** all of them are preserved after the new `skills` entry is added
