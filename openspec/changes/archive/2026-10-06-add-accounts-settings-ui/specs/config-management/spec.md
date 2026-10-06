## ADDED Requirements

### Requirement: Accounts may also be stored in the database

Accounts edited in Settings SHALL be persisted in the DB (an `accounts` table, plus the `config` keys `accounts.default` and `accounts.projects`) and loaded by `db.Config()` before `config.toml` is applied. `config.toml` SHALL override the DB per account name, and `default_account` / each `project_accounts` entry when present; names defined only in the DB SHALL remain. `inherit` SHALL remain `config.toml`-only.

#### Scenario: Toml overrides a DB account by name
- **WHEN** the DB holds account `work` and `config.toml` defines `[accounts.work]` with a different directory
- **THEN** `db.Config()` returns the `config.toml` directory and Settings marks `work` read-only

#### Scenario: DB-only accounts survive a toml overlay
- **WHEN** `config.toml` defines only `[accounts.work]` and the DB holds `personal`
- **THEN** both accounts are present in `db.Config()`
