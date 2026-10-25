## ADDED Requirements

### Requirement: Backend families declare bridge mod capability
Each backend `Family` SHALL declare whether Claude Code mods can be loaded into its sessions (`BridgeMod`); only the Claude family SHALL declare it. BuildCmd SHALL append ` --plugin-dir <materialized mod dir>` for a family with the capability when `[bridge] enabled` is true and materialization succeeds, and SHALL otherwise omit the flag without blocking launch.

#### Scenario: Claude launch
- **WHEN** BuildCmd builds a Claude command with the bridge enabled
- **THEN** the command contains `--plugin-dir` followed by the quoted materialized mod directory

#### Scenario: Non-Claude launch
- **WHEN** BuildCmd builds a Codex, Pi, OpenCode, Antigravity, Cursor or custom command
- **THEN** the command contains no `--plugin-dir`

#### Scenario: Bridge disabled
- **WHEN** `[bridge] enabled` is false
- **THEN** no `--plugin-dir` is added

#### Scenario: Materialization failure
- **WHEN** the mod cannot be materialized
- **THEN** the failure is logged and the command is built without `--plugin-dir`
