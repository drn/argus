# skill-provisioning Specification

## Purpose

Skill Provisioning owns the canonical `internal/skills/builtin/<name>/SKILL.md` bodies for argus-coupled skills (skills that drive `mcp__argus__*` tools, read `ARGUS_TASK_ID`/`~/.argus`, or encode the hera coordination model). It embeds those bodies directly into the binary and materializes them to backend-specific managed discovery paths, so every Argus-launched Claude, Codex, Pi, and OpenCode session receives them without a repository mirror, manual install, or global symlink. It is the discoverable-skill-body counterpart to `routing-provisioning`, which embeds orientation prose rather than on-demand `SKILL.md` bodies.
## Requirements
### Requirement: Builtin skill body bundle

The system SHALL treat `internal/skills/builtin/<name>/SKILL.md` as the sole in-repo source for each argus-coupled builtin skill and SHALL embed those bodies directly into the argus binary via `go:embed`, one `SKILL.md` per subdirectory under the embedded root. The system SHALL NOT require or maintain a same-named builtin mirror under a repository's `.agents/skills/` or `.claude/skills/` tree. The embedded set SHALL be derived generically by iterating the embedded directory tree (no hardcoded per-skill name list), so adding a new builtin skill requires only adding its canonical directory, not a code change or project-local copy. Genuinely project-specific skills MAY remain under `.agents/skills/` and are not part of the embedded set.

#### Scenario: Embedded set includes every shipped builtin

- **WHEN** the embedded builtin skill set is enumerated
- **THEN** it includes `argus-archive`, `argus-complete`, `argus-recycle`, `argus-resolve-model`, `argus-schedule`, `hera`, `hera-plan`, `hera-review`, `hera-review-test-adversary`, and `hera-spawn-review`

#### Scenario: Builtin has no repository-local mirror

- **WHEN** an embedded builtin name also exists beneath the Argus repository's `.agents/skills/` or `.claude/skills/` directory
- **THEN** the source-layout regression test fails, requiring the project-local duplicate to be removed

#### Scenario: Project-only skill remains independent

- **WHEN** a repository-specific skill such as `update-agent-models` is not present under `internal/skills/builtin/`
- **THEN** it MAY remain under `.agents/skills/` and is not materialized as an Argus builtin

### Requirement: Idempotent materialization for --add-dir delivery

The system SHALL materialize the embedded skill bodies to `~/.argus/skills/.claude/skills/<name>/`, rewriting only files whose content differs from what is already on disk, and SHALL remove materialized skill directories that no longer correspond to an embedded skill. The materializing function SHALL return the workspace root path on success, suitable for direct use as a `--add-dir` argument, and SHALL be inert (no filesystem writes, empty path, no error) when running inside a Go test binary.

#### Scenario: Materialization is inert during automated tests

- **WHEN** the materializing function runs inside a Go test binary
- **THEN** it returns an empty path and no error, performing no filesystem writes under `~/.argus/`

### Requirement: Argus-only Codex builtin skill materialization

The system SHALL materialize the same embedded builtin skill bodies used for the Claude-targeted `~/.argus/skills/.claude/skills/` workspace under `~/.local/share/argus/codex-home/skills/<name>/SKILL.md` for the default Codex home. A custom `CODEX_HOME` SHALL use a separate, stable `custom-<hash>` subdirectory under the Argus home so different source homes cannot share links. Argus SHALL set `CODEX_HOME` to this isolated home only for Codex sessions it launches. Ordinary Codex sessions SHALL not discover Argus's builtin skills through the user's normal `$CODEX_HOME/skills` directory. Materialization SHALL be idempotent (rewrite a file only when its content differs from what is already on disk).

The isolated home SHALL link the user's existing Codex configuration, authentication, session files, bundled system skills, and user-installed skills from the normal Codex home without modifying that home. Its skill directory SHALL not link previously global Argus-managed skills back into the isolated home. `CODEX_SQLITE_HOME` SHALL continue to point to the normal Codex state directory so existing session capture and resume keep working. Writing Argus skill bodies and removing stale Argus skill directories SHALL require a per-directory Argus ownership marker; a colliding unmarked user skill SHALL be preserved. On later launches, newly installed normal-home entries SHALL take precedence over matching overlay entries, with displaced overlay content preserved in the Argus home. Dangling links to removed user skills SHALL be removed so an Argus builtin can return. `.system/` SHALL never be written into or removed. This materialization SHALL be inert inside a Go test binary.

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

### Requirement: Session-scoped Pi and OpenCode builtin skills

Argus SHALL make its embedded skills available to Pi sessions through a `--skill` path and to OpenCode sessions through `OPENCODE_CONFIG_CONTENT` with an added `skills` path. These overrides SHALL apply only to Argus-launched child processes and SHALL preserve the user's existing Pi skills and OpenCode configuration. Argus SHALL NOT inject its skills path into OpenCode's global config. On daemon startup, it SHALL remove only the previously injected Argus skills path from that global config, preserving all other values and the Argus MCP registration. A malformed inherited `OPENCODE_CONFIG_CONTENT` SHALL remain untouched; OpenCode launch SHALL continue without Argus skills.

#### Scenario: Pi launch

- **WHEN** Argus launches a Pi session
- **THEN** the command receives the Argus-owned skills directory through `--skill`, including on resume
- **AND** ordinary Pi sessions and the user's `~/.pi` directory remain unchanged

#### Scenario: OpenCode launch

- **WHEN** Argus launches an OpenCode session with valid existing inline config
- **THEN** only the child receives `OPENCODE_CONFIG_CONTENT` containing the Argus skills path and all existing inline settings and skill paths
- **AND** ordinary OpenCode sessions do not receive that path through global config

#### Scenario: Remove old global OpenCode path

- **WHEN** the daemon starts with a previously injected Argus skills path in OpenCode's global config
- **THEN** it removes only that path and preserves other skills paths and the MCP server entry
