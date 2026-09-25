## MODIFIED Requirements

### Requirement: Non-Claude backend context prefix

For non-Claude backends, the system SHALL prepend a context block to the task's initial spawn prompt — the prompt actually delivered to the spawned process — containing only the builtin hera/routing orientation content Claude backends receive via `--append-system-prompt-file`.

Neither a **Codex** nor an **opencode** backend SHALL receive `CLAUDE.md` content (global or repo-local) in this block: both CLIs already have their own native instruction-file discovery — Codex reads a global `~/.codex/AGENTS.md` unconditionally plus repo-local `AGENTS.md` walking from cwd to the worktree root; opencode reads repo-local `CLAUDE.md` as an `AGENTS.md` fallback and the user's global `~/.claude/CLAUDE.md` as a documented compatibility fallback. Duplicating either source into the prompt prefix would be pure token cost with no discovery benefit neither CLI doesn't already provide itself.

This SHALL NOT apply to Claude-style backends, whose existing native `CLAUDE.md` discovery and `--add-dir`/`--append-system-prompt-file` injection are unaffected by this requirement, and SHALL NOT apply to the `pi` backend (out of scope).

#### Scenario: Codex backend receives routing orientation only

- **WHEN** a command is built for a Codex backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with the routing orientation content ahead of the original prompt text, and the prefix does not contain CLAUDE.md content

#### Scenario: opencode backend receives routing orientation only

- **WHEN** a command is built for an opencode backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with the routing orientation content ahead of the original prompt text, and the prefix does not contain CLAUDE.md content

#### Scenario: Claude backend is unaffected

- **WHEN** a command is built for a Claude-style backend
- **THEN** the prompt is not prefixed with this context block (Claude continues to rely on native `CLAUDE.md` discovery and the existing `--add-dir`/`--append-system-prompt-file` flags)

#### Scenario: pi backend is unaffected

- **WHEN** a command is built for the `pi` backend
- **THEN** the prompt is not prefixed with this context block
