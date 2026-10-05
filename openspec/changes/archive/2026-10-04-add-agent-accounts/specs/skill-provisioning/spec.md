## MODIFIED Requirements

### Requirement: Argus-only Codex builtin skill materialization

The system SHALL materialize the same embedded builtin skill bodies used for the Claude-targeted `~/.argus/skills/.claude/skills/` workspace under `~/.local/share/argus/codex-home/skills/<name>/SKILL.md` for the default Codex home. A custom `CODEX_HOME`, or a task account's `codex_home`, SHALL use a separate, stable `custom-<hash>` subdirectory under the Argus home so different source homes cannot share links. Argus SHALL set `CODEX_HOME` to this isolated home only for Codex sessions it launches. Ordinary Codex sessions SHALL not discover Argus's builtin skills through the user's normal `$CODEX_HOME/skills` directory. Materialization SHALL be idempotent (rewrite a file only when its content differs from what is already on disk).

The isolated home SHALL link the source Codex home's existing configuration, authentication, session files, bundled system skills, and user-installed skills without modifying that home or reading its authentication files. Its skill directory SHALL not link previously global Argus-managed skills back into the isolated home. `CODEX_SQLITE_HOME` SHALL point to the source home's state directory (the normal Codex home for the default account, the account's `codex_home` otherwise) so session capture and resume keep working. Writing Argus skill bodies and removing stale Argus skill directories SHALL require a per-directory Argus ownership marker; a colliding unmarked user skill SHALL be preserved. On later launches, newly installed source-home entries SHALL take precedence over matching overlay entries, with displaced overlay content preserved in the Argus home. Dangling links to removed user skills SHALL be removed so an Argus builtin can return. `.system/` SHALL never be written into or removed. This materialization SHALL be inert inside a Go test binary.

#### Scenario: Embedded skills materialized to the Codex-scoped path

- **WHEN** the Codex-scoped materialization runs
- **THEN** every embedded builtin skill's `SKILL.md` (and any accompanying files) appears under `~/.local/share/argus/codex-home/skills/<name>/`, matching the embedded source content, and each materialized directory carries Argus's ownership marker
- **AND** the normal Codex home receives no Argus skill files

#### Scenario: Stale argus-owned skill directories removed

- **WHEN** the Codex-scoped materialization runs and a directory exists under the isolated `skills/` directory that carries Argus's ownership marker from a prior run but whose name does not correspond to any currently-embedded skill
- **THEN** that directory is removed

#### Scenario: Foreign and reserved directories are never removed

- **WHEN** the Codex-scoped materialization runs and a directory exists under the isolated `skills/` directory that either is named `.system/` or lacks Argus's ownership marker — including a linked user-installed skill — and its name does not correspond to any currently-embedded skill
- **THEN** that directory and its contents are left untouched

#### Scenario: Name collision with a foreign directory is never claimed

- **WHEN** the Codex-scoped materialization runs and a directory already exists under the isolated `skills/` directory whose name matches a currently-embedded skill, but that directory lacks Argus's ownership marker
- **THEN** that directory's content is left completely untouched — not overwritten with the embedded skill's content, and not stamped with the ownership marker

#### Scenario: Materialization is inert during automated tests

- **WHEN** the Codex-scoped materialization runs inside a Go test binary
- **THEN** it performs no filesystem writes and returns no error

#### Scenario: Account home gets its own overlay

- **WHEN** a Codex task runs on an account whose `codex_home` is `~/.codex-work`
- **THEN** the overlay is a `custom-<hash>` directory that links only `~/.codex-work`'s entries, and `CODEX_SQLITE_HOME` is `~/.codex-work`
