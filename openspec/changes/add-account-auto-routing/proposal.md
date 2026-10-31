## Why

Argus picks a task's account once, statically (explicit → project pin → `default_account` → `default`), with no regard for usage, and hera workers always inherit their coordinator's account. With no global default configured, 19 of 21 live tasks ran on the default (work) login; on 2026-10-30 it hit 100% of its 5-hour window while the personal account sat at 0%, and every agent on it stalled with no way to move them short of waiting for the reset. The existing usage-aware routing only chooses between backends (Claude vs Codex), only reads the weekly window, and never chooses between accounts.

## What Changes

- New reserved account value `auto`, accepted for `default_account`, `project_accounts` entries, explicit task-create selections (TUI form, REST, MCP), and hera inheritance. At creation an `auto` task is resolved to the concrete account with the most usage headroom across both the 5-hour and weekly Claude windows, and the task is marked as auto-routed.
- New per-account usage ledger in the daemon, fed by the bridge's per-session `five_hour` / `seven_day` rate-limit readings. A reading stays valid until its own `resets_at`, so an account that hit its cap is skipped until it resets even after its sessions end. Accounts with no reading count as available.
- New `[account_routing]` config.toml table: `five_hour_threshold_pct` (default 90) and `weekly_threshold_pct` (default 90).
- Hera workers / sub-coordinators / freelancers and MCP `task_create` callers whose own account was auto-routed now auto-route too; a deliberately chosen account (explicit or project pin) is still inherited verbatim.
- New "move task to another account" operation for Claude tasks: stop the idle session, copy its transcript into the target account, switch the task's account, and resume the same conversation. Target may be a named account, `default`, or `auto`. Exposed via REST `POST /api/tasks/{id}/account`, MCP `task_move_account` (so a coordinator can move its agents on request), and a TUI `A` key on the task list and Hera rail (also in the command palette, and over REST in remote mode).
- `GET /api/accounts` gains an `auto` entry (when any named account is configured) and a per-account `usage` object.
- **BREAKING (semantics):** the "account resolved once at creation and immutable" invariant becomes "resolved once at creation; changed only by an explicit move".

## Capabilities

### New Capabilities

- `account-usage-routing`: the per-account usage ledger, the `auto` selection rule, thresholds, and the account-move operation.

### Modified Capabilities

- `agent-execution`: account resolution gains `auto` and auto-aware hera inheritance; account immutability relaxed to allow an explicit move.
- `config-management`: `auto` as a reserved account value; `[account_routing]` thresholds.
- `mcp-server`: `task_create` inheritance of auto-routed callers; new `task_move_account` tool.
- `rest-api`: `auto` entry and `usage` on `GET /api/accounts`; new `POST /api/tasks/{id}/account`.
- `forms-and-modals`: new-task account selector offers `auto`.
- `keybindings`: `A` moves the selected task to another account (task list and Hera rail).

## Impact

- Code: `internal/config` (reserved `auto`, `AccountRouting`), new `internal/accountusage` (ledger + selection), `internal/agent/create.go` + `hera_spawn.go` (resolution / inheritance), new `internal/agent/accountmove.go`, `internal/db` (`tasks.account_auto` column), `internal/model/task.go`, `internal/api` (ledger feed from bridge measures, `/api/accounts`, move endpoint), `internal/daemon` (ledger wiring + `AccountUsage` TUI RPC), `internal/daemon/client`, `internal/mcp` (`task_move_account`, `task_create` inheritance), `internal/tui` (form `auto`, `A` key + picker modal, palette, remote path via `apiclient`/`apistore`), `internal/tui/keymap` + help + README Reference.
- Wire: additive TUI↔daemon RPC method `AccountUsage` (old daemon → error → all-unknown, fail open). No supervisor protocol change.
- Frontend parity: web + macOS new-task forms get `auto` for free via the new `/api/accounts` entry. **Named follow-ups:** the move action in the web SPA and macOS app; a Settings-UI picker offering `auto` (config.toml-only in v1, like other routing keys); Codex transcript moves (Claude-only in v1); usage readings for accounts with no live sessions (no probe per named account in v1).
