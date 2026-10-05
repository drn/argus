## ADDED Requirements

### Requirement: Sandbox permits writes to the task's own account directory

The generated SBPL profile SHALL allow `file-write*` under the task's own account directory when it lies outside the tool's default directory: the account's `claude_config_dir` outside `$HOME/.claude` for Claude backends, and the account's `codex_home` outside `$HOME/.codex` for Codex backends. This keeps login token persistence and `.claude.json` atomic writes from failing with EPERM. The profile MUST NOT grant writes to any other configured account's directory. Credential read denies SHALL be unchanged.

#### Scenario: Account dir outside ~/.claude
- **WHEN** a Claude task runs on an account whose `claude_config_dir` is `~/.claude-personal`
- **THEN** the profile contains a write allow for that subpath

#### Scenario: Other accounts are not writable
- **WHEN** a task runs on account `work` and account `personal` is also configured
- **THEN** the profile has no write allow for `personal`'s directories
