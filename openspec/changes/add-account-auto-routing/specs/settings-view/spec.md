## MODIFIED Requirements

### Requirement: Accounts are viewable and editable in Settings

The TUI Settings view SHALL provide an **Accounts** category listing a default-account row, every account (with its `label`, `claude_config_dir` and `codex_home`), and one row per project showing that project's default account. `n` SHALL add an account (inline name prompt; the new account is seeded with `claude_config_dir = ~/.claude-<name>`), `d` SHALL delete the selected account, Enter or `e` SHALL edit the selected field inline, and →/Enter on the default-account and project rows SHALL cycle through the valid account names (including `default`, and `auto` whenever at least one account is configured); a stored `auto` SHALL NOT be shown as `(missing)`. Every edit SHALL be validated with `Config.ValidateAccount` before it is stored; a rejected edit SHALL leave the stored value unchanged and show the reason in the pane. Edits SHALL take effect for the next task created without a restart.

#### Scenario: Add an account
- **WHEN** the user presses `n` in Accounts and enters `personal`
- **THEN** an account `personal` with `claude_config_dir = ~/.claude-personal` is stored and appears in the list and in the new-task Account selector

#### Scenario: Cycle the default to auto
- **WHEN** an account `personal` exists and the user cycles the default-account row
- **THEN** the order is `default`, `auto`, `personal`, and choosing `auto` stores it and shows `Default account: auto` with no `(missing)` marker

#### Scenario: Deleting an account resets references to it
- **WHEN** the user deletes an account that is the stored default or a project's stored default
- **THEN** those selections are reset (config.toml-defined ones are left and shown as `(missing)`), and the confirmation names how many tasks and project defaults are affected

#### Scenario: Two accounts cannot share a directory
- **WHEN** an edit would give an account the same `claude_config_dir` or `codex_home` as another account
- **THEN** the edit is rejected, since a shared directory means a shared login

#### Scenario: Invalid directory is rejected
- **WHEN** the user edits `claude_config_dir` to `~` (the home directory)
- **THEN** the value is not stored and the pane shows why

#### Scenario: Name rules
- **WHEN** the user enters `default`, an empty name, an existing name, or a name containing characters other than letters, digits, `-` and `_`
- **THEN** no account is created and the reason is shown
