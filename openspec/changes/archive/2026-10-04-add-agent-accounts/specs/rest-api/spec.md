## ADDED Requirements

### Requirement: Accounts exposed over REST

`POST /api/tasks` (JSON and multipart) SHALL accept an optional `account` (unknown name, or an account that does not support an explicitly requested backend → 400) and task JSON SHALL include `account`. `GET /api/accounts` SHALL return a bare JSON array, `default` first then configured accounts by name, of `{name, label, claude_config_dir, codex_home, supports: {claude, codex}, is_default, logged_in, email?, org?, plan?, codex_logged_in?}`. An optional `?project=` SHALL make `is_default` mark that project's resolved default. The `logged_in` / `email` / `org` / `plan` fields describe the Claude login (best-effort, empty for an account without `claude_config_dir`); `codex_logged_in` is omitted when unknown or when the account has no `codex_home`. Sign-in lookups SHALL run under a short budget and degrade rather than fail. The endpoint SHALL never return credentials. `GET /api/skills` SHALL accept optional `account` and `task` parameters selecting whose Claude config dir to read skills from (unknown account or task → 400; no `claude_config_dir` → `~/.claude`).

#### Scenario: Unknown account on create
- **WHEN** a client POSTs `account: "nope"`
- **THEN** the response is 400 and no task is created

#### Scenario: Account lacking the requested backend's tool
- **WHEN** a client POSTs `backend` naming a codex backend and `account` naming a Claude-only account
- **THEN** the response is 400 and no task is created

#### Scenario: Project-aware default
- **WHEN** a client requests `GET /api/accounts?project=acme` and `project_accounts` maps `acme` to `work`
- **THEN** only `work` has `is_default: true`
