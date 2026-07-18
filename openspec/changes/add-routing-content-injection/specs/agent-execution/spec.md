## ADDED Requirements

### Requirement: Builtin routing content auto-injected via system prompt file

The system SHALL make argus's builtin hera-coordination and argus-task-management routing content available to every spawned Claude-backend session by materializing it to a stable file under `~/.argus` and appending Claude Code's `--append-system-prompt-file <path>` flag to the command. This SHALL apply unconditionally to every session kind (coordinator, worker, freelance) and SHALL NOT be gated on `cfg.Hera.Enabled` or any per-project/per-task configuration — the injected content is self-gating at read time (each section opens with an `ARGUS_TASK_ID`/`$PWD` sandbox-residency check), so appending it to a non-argus-context spawn is inert. Materialization failure SHALL be logged and SHALL NOT block session launch; the flag is appended only when materialization succeeds.

#### Scenario: Claude backend receives the routing system-prompt flag

- **WHEN** a command is built for a Claude backend and routing-content materialization succeeds
- **THEN** the command appends `--append-system-prompt-file` followed by the materialized path

#### Scenario: Non-Claude backends are unaffected

- **WHEN** a command is built for a non-Claude backend (codex, pi, opencode, or a bare custom command)
- **THEN** no `--append-system-prompt-file` flag is appended, regardless of whether routing-content materialization would have succeeded

#### Scenario: Materialization failure does not block launch

- **WHEN** the routing content cannot be materialized (e.g. a filesystem error)
- **THEN** command construction still succeeds without the `--append-system-prompt-file` flag, and the failure is logged
