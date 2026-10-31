## MODIFIED Requirements

### Requirement: Accounts exposed over REST

`POST /api/tasks` (JSON and multipart) SHALL accept an optional `account` (`"auto"` included; unknown name, or an account that does not support an explicitly requested backend → 400) and task JSON SHALL include `account` and `account_auto`. `GET /api/accounts` SHALL return a bare JSON array, `default` first, then an `auto` entry when at least one configured account is valid, then configured accounts by name, of `{name, label, claude_config_dir, codex_home, supports: {claude, codex}, is_default, logged_in, email?, org?, plan?, codex_logged_in?, usage?}`. The `auto` entry SHALL have label `Auto (most headroom)`, empty directories, and `supports` true for a tool when any configured account supports it. An optional `?project=` SHALL make `is_default` mark that project's resolved default (the `auto` entry when it resolves to `auto`). The `logged_in` / `email` / `org` / `plan` fields describe the Claude login (best-effort, empty for an account without `claude_config_dir`); `codex_logged_in` is omitted when unknown or when the account has no `codex_home`. `usage` SHALL carry the ledger's currently valid readings for that account as `{five_hour_pct?, five_hour_resets_at?, weekly_pct?, weekly_resets_at?}` and SHALL be omitted when none is valid or for the `auto` entry. Sign-in lookups SHALL run under a short budget and degrade rather than fail. The endpoint SHALL never return credentials. `GET /api/skills` SHALL accept optional `account` and `task` parameters selecting whose Claude config dir to read skills from (unknown account or task → 400; no `claude_config_dir` → `~/.claude`).

#### Scenario: Unknown account on create
- **WHEN** a client POSTs `account: "nope"`
- **THEN** the response is 400 and no task is created

#### Scenario: Account lacking the requested backend's tool
- **WHEN** a client POSTs `backend` naming a codex backend and `account` naming a Claude-only account
- **THEN** the response is 400 and no task is created

#### Scenario: Project-aware default
- **WHEN** a client requests `GET /api/accounts?project=acme` and `project_accounts` maps `acme` to `work`
- **THEN** only `work` has `is_default: true`

#### Scenario: Auto entry is the default
- **WHEN** `default_account = "auto"` and a client requests `GET /api/accounts`
- **THEN** the `auto` entry has `is_default: true`

#### Scenario: Usage reported
- **WHEN** the ledger holds `default` at 100% of its 5-hour window resetting at 17:40
- **THEN** the `default` entry's `usage.five_hour_pct` is 100 and `usage.five_hour_resets_at` is 17:40

## ADDED Requirements

### Requirement: Move a task to another account over REST

`POST /api/tasks/{id}/account` with JSON `{"account": "<name>|default|auto"}` SHALL perform the account-move operation and then resume the task's session as `POST /api/tasks/{id}/restart` does, responding 200 with `{from, to, resumed}`. An unknown task SHALL return 404; a live, non-idle session SHALL return 409; every other rejection (unknown or unsupported target, same account, non-Claude backend, no other candidate for `auto`) SHALL return 400 with the reason. A failure to restart after the move has persisted SHALL return 500 with the task left on the target account.

#### Scenario: Move succeeds
- **WHEN** a client POSTs `{"account": "personal"}` for an idle task on `default`
- **THEN** the response is 200 with `from: "default"`, `to: "personal"`, and the task runs on `personal`

#### Scenario: Busy task
- **WHEN** the task's session is actively producing output
- **THEN** the response is 409 and nothing changes
