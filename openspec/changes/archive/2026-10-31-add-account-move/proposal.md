## Why

When an account hits its 5-hour or weekly Claude limit, every agent on it stalls, and the stuck agent cannot help because any turn needs usage on that account. Routing (`protect-claude-quota`) now places new tasks on an account with room, but nothing rescues a task that is already running on a capped account. Separately, a coordinator that routing placed on an overflow account (e.g. `personal`) stamps that account onto every worker it spawns, so its workers stay pinned there even after that account fills up.

## What changes

- An account-move operation for Claude tasks: copy the task's newest transcript into the target account's Claude config dir, switch the task's account, and restart the same task in place with `--resume`, keeping its row, worktree, branch and hera bindings. It needs no agent turn, so it works when the current account has no usage left. Targets are a configured account, `default`, or `auto` (the other Claude-capable account with the lowest current usage).
- Surfaces: TUI `>` on the task list and the Hera rail (picker showing each account's 5-hour and weekly usage), MCP `task_move_account` for coordinators, and REST `POST /api/tasks/{id}/account`.
- `GET /api/accounts` reports each account's current 5-hour and weekly usage.
- A task whose account came from tier routing is flagged routed; its hera workers, sub-coordinators and MCP `task_create` children inherit a blank account and are routed themselves instead of copying the parent's account. An explicit or project account pin is still inherited verbatim. A move clears the flag.

## Capabilities

### New capabilities

- `account-move`: moving a Claude task's session to another account.

### Modified capabilities

- `agent-execution`: the stored account changes only through a move; routed parents let children route.
- `mcp-server`: routed callers let `task_create` children route; new `task_move_account` tool.
- `rest-api`: per-account usage on `GET /api/accounts`; new move endpoint.
- `keybindings`: `>` move-to-account on the task list and Hera rail.

## Impact

`internal/agent` (move operation, inheritance), `internal/db` (`tasks.account_routed`, targeted account update), `internal/usagebudget` (per-account reading accessor), `internal/api`, `internal/mcp`, `internal/daemon` (+ client RPC for per-account usage), `internal/apiclient`/`apistore`, `internal/tui` (picker, keymap, help), README Reference, gotchas. No supervisor protocol change: `KickReq` already carries `Account`.

## Non-goals

- Automatic moves. Moves are manual or coordinator-initiated.
- Named follow-up `web-macos-account-move`: the web and macOS clients get no move button in this change; both can reach it over REST.
- Named follow-up `codex-account-move`: Codex tasks cannot move (no transcript copy path for Codex homes yet).
