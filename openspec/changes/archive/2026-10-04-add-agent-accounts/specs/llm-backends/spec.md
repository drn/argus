## ADDED Requirements

### Requirement: Codex runs per account through CODEX_HOME

A Codex task on a named account SHALL run with that account's `codex_home` as its source Codex home: Argus SHALL build a separate overlay home for it, export it as `CODEX_HOME`, and export `CODEX_SQLITE_HOME=<codex_home>` so session capture and `codex resume` read that account's session database. The argus MCP server SHALL reach the session through the same per-launch `-c mcp_servers.argus.url=…` flag as a default-account session, and nothing SHALL be written to the account's `config.toml`. The Codex sign-in state SHALL be probed only via the exit status of `codex login status` (output discarded unread, short timeout, briefly cached, inert inside a Go test binary); `auth.json` SHALL never be read. The Codex usage probe SHALL measure the default account only.

#### Scenario: First use of a Codex account
- **WHEN** a Codex task first runs on account `work` whose `codex_home` does not exist
- **THEN** the directory is created with mode 0700, nothing is copied from `~/.codex`, and the user signs in once with `CODEX_HOME=<codex_home> codex login` from a terminal

#### Scenario: Session capture reads the account database
- **WHEN** a Codex task on account `work` exits
- **THEN** its session ID is captured from `work`'s `state_5.sqlite`, not `~/.codex`'s
