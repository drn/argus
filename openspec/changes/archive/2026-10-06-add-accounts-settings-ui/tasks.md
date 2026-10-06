# Tasks — add-accounts-settings-ui

- [x] db: `accounts` table; `Accounts/SetAccount/DeleteAccount`; default + project map in `config` kv; `db.Config()` loads them; tests
- [x] config: FileLoader records which account names / default / project entries `config.toml` defines; `db.DB` accessor; tests
- [x] settings: `catAccounts` rows, inline field edit, add/delete, default + project cycle, read-only markers, remote read-only, validation errors; tests
- [x] uxlog on every edit/reject; smoke test through the real event loop
- [x] docs: README Reference, gotchas, index; archive this change in the PR
