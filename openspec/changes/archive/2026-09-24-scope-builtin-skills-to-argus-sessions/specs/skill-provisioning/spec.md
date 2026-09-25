## MODIFIED Requirements

### Requirement: Argus-only Codex builtin skill materialization

The system SHALL place Argus's embedded skills under `~/.local/share/argus/codex-home/skills/` for the default Codex home, or a stable `custom-<hash>` subdirectory for a custom `CODEX_HOME`, and SHALL leave the user's normal `$CODEX_HOME/skills/` untouched. It SHALL link ordinary Codex configuration, authentication, session files, system skills, and user-installed skills into the isolated home, while preserving name collisions and protecting unmarked and `.system` skill directories. Distinct source homes SHALL have distinct overlays. Argus SHALL set the isolated `CODEX_HOME` only for Codex processes it launches.

#### Scenario: Ordinary Codex has no Argus builtin skills

- **WHEN** ordinary Codex starts without Argus's child-only `CODEX_HOME` override
- **THEN** it does not discover Argus's embedded skills through the normal Codex home

#### Scenario: Argus Codex sees builtin and user skills

- **WHEN** an Argus Codex session starts after isolated-home materialization
- **THEN** it discovers the embedded Argus skills together with the user's system and other installed skills
