## ADDED Requirements

### Requirement: Non-Claude backend context prefix

For non-Claude backends (Codex, opencode), the system SHALL prepend a context block to the task's initial spawn prompt — the prompt actually delivered to the spawned process — containing, when available: the global `~/.claude/CLAUDE.md` content, the repo-local `CLAUDE.md` content at the worktree root, the same builtin hera/routing orientation content Claude backends receive via `--append-system-prompt-file`, and a skill catalog (name + one-line description per skill). This SHALL NOT apply to Claude-style backends, whose existing native `CLAUDE.md` discovery and `--add-dir`/`--append-system-prompt-file` injection are unaffected by this requirement, and SHALL NOT apply to the `pi` backend (out of scope; `pi` has no MCP wiring to make the skill catalog actionable). A source that is unavailable (no global or repo `CLAUDE.md` file, no discoverable skills) SHALL be omitted from the block cleanly rather than rendered as an empty or placeholder section.

#### Scenario: Codex backend receives the context prefix

- **WHEN** a command is built for a Codex backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with the context block (global CLAUDE.md, repo CLAUDE.md, routing orientation, skill catalog) ahead of the original prompt text

#### Scenario: opencode backend receives the context prefix

- **WHEN** a command is built for an opencode backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process is prefixed with the context block ahead of the original prompt text

#### Scenario: Claude backend is unaffected

- **WHEN** a command is built for a Claude-style backend
- **THEN** the prompt is not prefixed with this context block (Claude continues to rely on native `CLAUDE.md` discovery and the existing `--add-dir`/`--append-system-prompt-file` flags)

#### Scenario: pi backend is unaffected

- **WHEN** a command is built for the `pi` backend
- **THEN** the prompt is not prefixed with this context block

#### Scenario: Missing CLAUDE.md files omitted cleanly

- **WHEN** a command is built for a non-Claude backend and neither a global `~/.claude/CLAUDE.md` nor a repo-local `CLAUDE.md` exists
- **THEN** the context block omits both sections cleanly rather than emitting an empty or placeholder section for either

#### Scenario: No discoverable skills omitted cleanly

- **WHEN** a command is built for a non-Claude backend and the skill catalog is empty
- **THEN** the context block omits the skill catalog section cleanly rather than emitting an empty or placeholder section
