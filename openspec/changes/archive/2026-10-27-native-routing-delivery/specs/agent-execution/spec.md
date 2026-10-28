## REMOVED Requirements

### Requirement: Non-Claude backend context prefix

## ADDED Requirements

### Requirement: Routing orientation is delivered natively, never as a prompt prefix

The system SHALL NOT prepend any Argus-generated context block (routing orientation, `CLAUDE.md` content, or otherwise) to a task's initial spawn prompt for any backend; the prompt delivered to the spawned process SHALL be exactly the task's prompt. Argus's builtin routing orientation SHALL instead reach each backend through that backend's own instruction mechanism: Claude via `--append-system-prompt-file`; Pi via `--append-system-prompt`; OpenCode via an `instructions` entry in its child-only inline config (preserving any user entries); Codex via a generated `AGENTS.md` in its Argus-only `CODEX_HOME` overlay containing the user's own global instructions (`AGENTS.override.md` taking precedence over `AGENTS.md`, neither linked nor modified) followed by the routing content; Antigravity via an Argus-owned always-on rule file under `~/.gemini/config/rules/`. A backend with no such mechanism (Cursor) receives no routing orientation. Delivery failures SHALL be logged and SHALL NOT block launch.

#### Scenario: Prompt is delivered unmodified

- **WHEN** a command is built for any backend with a non-empty prompt
- **THEN** the prompt delivered to the spawned process contains only the original prompt text

#### Scenario: Pi receives routing through a flag

- **WHEN** a Pi command is built and the routing file materializes
- **THEN** the command contains `--append-system-prompt` naming that file

#### Scenario: OpenCode instructions preserve user entries

- **WHEN** an OpenCode command is built and the inherited inline config already has an `instructions` array
- **THEN** the routing file is appended to that array without removing or duplicating entries

#### Scenario: Codex overlay AGENTS.md folds in the user's instructions

- **WHEN** the Argus-only Codex home is prepared and the user's Codex home has an `AGENTS.md` (or `AGENTS.override.md`)
- **THEN** the overlay's `AGENTS.md` is a generated file holding the user's content followed by the routing content, the user's files are unchanged, and no `AGENTS.override.md` link exists in the overlay
