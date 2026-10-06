# Edit accounts from the TUI Settings view

## Why

Accounts (add-agent-accounts) are `config.toml`-only, so adding a personal account
means hand-editing a TOML file the user may not know about. This is the
"Settings-UI editing" follow-up that change named in its Non-Goals.

## What Changes

- New **Accounts** category in the TUI Settings view: a default-account row, one
  block per account (name header + editable `label`, `claude_config_dir`,
  `codex_home` rows), and one row per project to pick that project's default
  account. `n` adds an account (seeded with `~/.claude-<name>`), `d` deletes,
  Enter/`e` edits a field inline, ←/→/Enter cycles the default / a project's account.
- Accounts edited here are stored in the DB (new `accounts` table; the default and
  per-project mapping live in the `config` kv table) and take effect for the very
  next task, since the daemon re-reads config per call.
- `config.toml` keeps winning: an account name, `default_account`, or a
  `project_accounts` entry defined in the file overrides the DB value per key and is
  shown read-only with a `(config.toml)` marker. `inherit` stays `config.toml`-only.
- Every edit is validated with the same rules as the file (`Config.ValidateAccount`:
  at least one dir, absolute or `~/`, not a tool default / `$HOME` / sensitive dir);
  a rejected edit leaves the stored value unchanged and shows the reason.

## Non-Goals (named follow-ups)

- **Web SPA and macOS account editing.** No REST write endpoints for accounts exist;
  the TUI edits the DB directly (like backend tiers), so `--remote` mode shows the
  daemon's accounts read-only. A REST surface + web/macOS editors is the follow-up.
- Editing `inherit` from the UI.
- Deleting an account that tasks already use is allowed; those tasks then fail
  loudly on resume (add-agent-accounts D3), so the delete confirms the usage count.

## Impact

`internal/db` (schema, accounts.go, config.go), `internal/config/file.go`,
`internal/tui/settings.go`, specs `settings-view` + `config-management`, README
Reference, gotchas.
