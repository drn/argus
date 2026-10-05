## ADDED Requirements

### Requirement: Accounts are configured as named tool directories

The system SHALL support `[accounts.<name>]` tables in `config.toml`, each with an optional `label`, an optional `claude_config_dir` (Claude Code `CLAUDE_CONFIG_DIR`), an optional `codex_home` (Codex `CODEX_HOME`), and an optional `inherit` list of entries symlinked from `~/.claude` into the Claude config dir (default `CLAUDE.md`, `skills`, `commands`, `agents`; an explicit empty list inherits nothing). An account MUST define at least one of `claude_config_dir` / `codex_home`, and each defined dir MUST be absolute or `~`-prefixed. The reserved name `default` SHALL denote each tool's own default directory (`~/.claude`, `~/.codex`) and MUST NOT be redefinable. A global `default_account` and a top-level `project_accounts` map (project name to account) SHALL select the default for new tasks. An account SHALL support a Claude backend only when it defines `claude_config_dir`, a Codex backend only when it defines `codex_home`, and no other backend (pi, opencode, custom commands); `default` supports every backend. Accounts SHALL be defined in `config.toml` only.

#### Scenario: Unknown default account is rejected
- **WHEN** `default_account` or a `project_accounts` entry names an account not defined
- **THEN** config validation reports the error and the value is ignored (resolves to `default`)

#### Scenario: Relative dir is rejected
- **WHEN** an account's `claude_config_dir` or `codex_home` is neither absolute nor `~`-prefixed
- **THEN** that account is invalid and unavailable for selection

#### Scenario: Account with neither dir is rejected
- **WHEN** an account defines neither `claude_config_dir` nor `codex_home`
- **THEN** that account is invalid and unavailable for selection

#### Scenario: Nothing configured
- **WHEN** no `accounts` exist
- **THEN** behavior is identical to before (no `CLAUDE_CONFIG_DIR` / account `CODEX_HOME` exported, no selector shown)

### Requirement: Account directories inherit shared entries but never credentials

On first use the system SHALL create the account's Claude config directory with mode 0700 and symlink each existing `inherit` entry that is absent in it. It SHALL seed `settings.json` as a one-time copy of `~/.claude/settings.json` (mode 0600) only when no entry of any kind exists at that path, never overwriting and never writing through a symlink, and SHALL drop `apiKeyHelper`, `forceLoginMethod`, `forceLoginOrgUUID`, `awsAuthRefresh`, `awsCredentialExport`, `otelHeadersHelper` and the entire `env` block from the copy. An account directory equal to the tool's own default directory, `$HOME`, `/`, an ancestor of `$HOME`, or a location inside `~/.ssh`, `~/.argus`, `~/.aws`, `~/.gnupg`, `~/.kube` or `~/Library` SHALL be rejected. Never-inherit entry names SHALL match case-insensitively. An unparsable source SHALL be skipped with a log. The system MUST NOT inherit `.credentials*`, `.claude.json`, `projects/`, or `plugins/`, and MUST NOT read, copy, log, or write account credentials (Claude `.credentials.json` / Keychain, Codex `auth.json`).

#### Scenario: Existing entry is not overwritten
- **WHEN** the account dir already contains `settings.json` (file or symlink)
- **THEN** it is left untouched

#### Scenario: Auth overrides are not seeded
- **WHEN** `~/.claude/settings.json` defines `apiKeyHelper`
- **THEN** the account's seeded `settings.json` does not contain it
