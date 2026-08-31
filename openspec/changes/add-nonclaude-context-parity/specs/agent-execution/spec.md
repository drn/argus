## ADDED Requirements

### Requirement: Non-Claude backend context prefix

For non-Claude backends, the system SHALL prepend a context block to the task's initial spawn prompt — the prompt actually delivered to the spawned process — whose contents differ by backend:

- For a **Codex** backend, the block SHALL contain, when available: the global `~/.claude/CLAUDE.md` content, the repo-local `CLAUDE.md` content at the worktree root, and the same builtin hera/routing orientation content Claude backends receive via `--append-system-prompt-file`.
- For an **opencode** backend, the block SHALL contain only the builtin hera/routing orientation content. It SHALL NOT contain CLAUDE.md content: opencode's own instruction-file discovery already reads repo-local `CLAUDE.md` whenever no repo-local `AGENTS.md` is present, and the user's global `~/.claude/CLAUDE.md` whenever no global `~/.config/opencode/AGENTS.md` is present — a precedence opencode resolves itself, not an unconditional read this requirement needs to duplicate or account for.

This SHALL NOT apply to Claude-style backends, whose existing native `CLAUDE.md` discovery and `--add-dir`/`--append-system-prompt-file` injection are unaffected by this requirement, and SHALL NOT apply to the `pi` backend (out of scope). A source that is unavailable to a backend that would otherwise include it (no global or repo `CLAUDE.md` file, for Codex) SHALL be omitted from the block cleanly rather than rendered as an empty or placeholder section.

#### Scenario: Codex backend receives the full context prefix

- **WHEN** a command is built for a Codex backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with a block containing global CLAUDE.md content, repo CLAUDE.md content, and routing orientation, ahead of the original prompt text

#### Scenario: opencode backend receives routing orientation only

- **WHEN** a command is built for an opencode backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with the routing orientation content ahead of the original prompt text, and the prefix does not contain CLAUDE.md content

#### Scenario: Claude backend is unaffected

- **WHEN** a command is built for a Claude-style backend
- **THEN** the prompt is not prefixed with this context block (Claude continues to rely on native `CLAUDE.md` discovery and the existing `--add-dir`/`--append-system-prompt-file` flags)

#### Scenario: pi backend is unaffected

- **WHEN** a command is built for the `pi` backend
- **THEN** the prompt is not prefixed with this context block

#### Scenario: Missing CLAUDE.md files omitted cleanly for Codex

- **WHEN** a command is built for a Codex backend and neither a global `~/.claude/CLAUDE.md` nor a repo-local `CLAUDE.md` exists
- **THEN** the context block omits both CLAUDE.md sections cleanly rather than emitting an empty or placeholder section for either, while still including routing orientation
