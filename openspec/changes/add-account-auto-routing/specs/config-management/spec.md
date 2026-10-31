## MODIFIED Requirements

### Requirement: Accounts are configured as named tool directories

The system SHALL support `[accounts.<name>]` tables in `config.toml`, each with an optional `label`, an optional `claude_config_dir` (Claude Code `CLAUDE_CONFIG_DIR`), an optional `codex_home` (Codex `CODEX_HOME`), and an optional `inherit` list of entries symlinked from `~/.claude` into the Claude config dir (default `CLAUDE.md`, `skills`, `commands`, `agents`; an explicit empty list inherits nothing). An account MUST define at least one of `claude_config_dir` / `codex_home`, and each defined dir MUST be absolute or `~`-prefixed. The reserved name `default` SHALL denote each tool's own default directory (`~/.claude`, `~/.codex`) and MUST NOT be redefinable. The reserved name `auto` SHALL denote usage-based selection at task creation, SHALL be accepted wherever an account is selected for a new task (`default_account`, `project_accounts` entries, explicit selections), and MUST NOT be definable as an `[accounts.auto]` table. A global `default_account` and a top-level `project_accounts` map (project name to account) SHALL select the default for new tasks. An `[account_routing]` table SHALL accept `five_hour_threshold_pct` and `weekly_threshold_pct` (integers 1..100, default 90 each; 0 or absent means unset); any other out-of-range value SHALL be ignored with a logged warning and the default used. An account SHALL support a Claude backend only when it defines `claude_config_dir`, a Codex backend only when it defines `codex_home`, and no other backend (pi, opencode, custom commands); `default` supports every backend. Accounts SHALL be defined in `config.toml` only.

#### Scenario: Unknown default account is rejected
- **WHEN** `default_account` or a `project_accounts` entry names an account not defined
- **THEN** config validation reports the error and the value is ignored (resolves to `default`)

#### Scenario: Auto is a valid default
- **WHEN** `default_account = "auto"` and no project entry applies
- **THEN** new tasks resolve to `auto` and are placed by the auto selection rule

#### Scenario: Auto cannot be defined as a table
- **WHEN** `config.toml` defines `[accounts.auto]`
- **THEN** that table is invalid and `auto` keeps its reserved meaning

#### Scenario: Out-of-range threshold
- **WHEN** `[account_routing] five_hour_threshold_pct = 150`
- **THEN** a warning is logged and 90 is used

#### Scenario: Relative dir is rejected
- **WHEN** an account's `claude_config_dir` or `codex_home` is neither absolute nor `~`-prefixed
- **THEN** that account is invalid and unavailable for selection

#### Scenario: Account with neither dir is rejected
- **WHEN** an account defines neither `claude_config_dir` nor `codex_home`
- **THEN** that account is invalid and unavailable for selection

#### Scenario: Nothing configured
- **WHEN** no `accounts` exist
- **THEN** behavior is identical to before (no `CLAUDE_CONFIG_DIR` / account `CODEX_HOME` exported, no selector shown)
