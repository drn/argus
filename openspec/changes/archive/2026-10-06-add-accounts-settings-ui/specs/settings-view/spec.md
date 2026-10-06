## ADDED Requirements

### Requirement: Accounts are viewable and editable in Settings

The TUI Settings view SHALL provide an **Accounts** category listing a default-account row, every account (with its `label`, `claude_config_dir` and `codex_home`), and one row per project showing that project's default account. `n` SHALL add an account (inline name prompt; the new account is seeded with `claude_config_dir = ~/.claude-<name>`), `d` SHALL delete the selected account, Enter or `e` SHALL edit the selected field inline, and →/Enter on the default-account and project rows SHALL cycle through the valid account names (including `default`). Every edit SHALL be validated with `Config.ValidateAccount` before it is stored; a rejected edit SHALL leave the stored value unchanged and show the reason in the pane. Edits SHALL take effect for the next task created without a restart.

#### Scenario: Add an account
- **WHEN** the user presses `n` in Accounts and enters `personal`
- **THEN** an account `personal` with `claude_config_dir = ~/.claude-personal` is stored and appears in the list and in the new-task Account selector

#### Scenario: Invalid directory is rejected
- **WHEN** the user edits `claude_config_dir` to `~` (the home directory)
- **THEN** the value is not stored and the pane shows why

#### Scenario: Name rules
- **WHEN** the user enters `default`, an empty name, an existing name, or a name containing characters other than letters, digits, `-` and `_`
- **THEN** no account is created and the reason is shown

### Requirement: config.toml-defined account settings are read-only in Settings

An account name, `default_account`, or `project_accounts` entry defined in `config.toml` SHALL be shown with a `(config.toml)` marker and SHALL NOT be editable, renamed, or deleted from Settings. In `--remote` mode the whole category SHALL be read-only.

#### Scenario: Toml account cannot be edited
- **WHEN** `config.toml` defines `[accounts.work]`
- **THEN** its rows show `(config.toml)` and edit/delete are refused
