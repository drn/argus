## ADDED Requirements

### Requirement: New-task form offers an account selector

The new-task form (TUI, web, macOS) SHALL show an "Account" selector listing `default` plus the configured accounts that support the selected backend, preselected from the project then global default, and labelled with the account's label and Claude signed-in identity when known. The selector SHALL be hidden when only `default` applies, and SHALL re-filter when the backend changes, keeping the user's pick if it still applies. A project change SHALL re-preselect that project's default. When the selector is shown, the form SHALL submit the selected name, `"default"` included; when hidden it SHALL submit an empty value. Skill autocomplete SHALL read skills from the effective account's Claude config dir. In remote mode the TUI SHALL take account names and labels from the daemon. The selector MUST implement paste/keyboard handling consistent with the other selectors.

#### Scenario: Selector hidden with no accounts
- **WHEN** no accounts are configured
- **THEN** the form has no Account field and tab order is unchanged

#### Scenario: Project default preselected
- **WHEN** `project_accounts` maps the selected project to `personal`
- **THEN** the selector starts on `personal`

#### Scenario: Backend filters the list
- **WHEN** the selected backend is codex and account `work` defines only `claude_config_dir`
- **THEN** `work` is not offered
