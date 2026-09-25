## Why

There is no way to see what messages a task has received without going
through an agent (`task_inbox` / `hera_inbox` MCP tools) or the REST API. Those
tools are also destructive for debugging: the hera inbox only returns UNREAD
messages, and reading through an agent usually acks them. When iterating on the
messaging / reliable-delivery machinery, the operator needs to look at a task's
inbox directly, including already-read messages and their delivery state,
without disturbing it.

## What Changes

- **New `i` hotkey on the Tasks list (`tasklist.inbox`) and on the Hera rail
  (`hera_rail.inbox`)** opens a read-only **Inbox** modal for the selected task.
  On the Hera rail it targets the selected row's bound task (no-op on a row with
  no task, e.g. a planned node or an orchestrator header with no coordinator).
- **The modal shows both message stores for that task, merged oldest-first:**
  - `task_messages` addressed to the task (read AND unread, up to 500), and
  - `hera_messages` addressed to ANY hera role the task is or was bound to
    (read AND unread), resolved via a `hera_bindings` subquery.
- **Each entry shows:** timestamp, source (`task` / `hera`), sender (task name,
  or role name for hera; the daemon system sender renders as `system`), kind
  (task) or tldr (hera), read state (`unread` / `read <time>`), for hera the
  delivery mode + delivered time, and the full body (wrapped).
- **Viewing is strictly read-only.** Opening, scrolling, or refreshing the modal
  never acks a `task_messages` row or stamps `read_at` on a `hera_messages` row.
- **Keys inside the modal:** `j`/`k`/arrows scroll a line, PgUp/PgDn scroll a
  page, `g`/`G` top/bottom, `r` reloads, Esc/`q`/`i` closes. The modal opens
  scrolled to the bottom (newest).
- **Loads off the UI thread** (DB reads in a goroutine, delivered via
  `QueueUpdateDraw`), with a "Loading…" state and an error line on failure.
  uxlog `[inbox]` logs open/load count/error.
- New DB read method `HeraMessagesForTask(taskID string, limit int)` — all
  read states, oldest first. `task_messages` uses the existing `Inbox` with
  `UnreadOnly=false, Limit=MaxInboxLimit`.

## Non-Goals

- **Remote TUI (`--remote`)**: local-only (`*db.DB` type-assert, like other
  local-only TUI ops). In remote mode the modal shows "Inbox viewer is not
  available in remote mode." Follow-up: expose hera messages over REST.
- **Web SPA and macOS app parity**: not in this change. `GET
  /api/tasks/{id}/inbox` already exists for `task_messages`; hera messages have
  no REST endpoint. Named follow-up: `add-inbox-view-web-macos` (add a read-only
  hera-messages endpoint, then an inbox panel in both clients).
- Sending, replying, acking, or deleting from the modal. Outgoing (sent)
  messages. Live auto-refresh (`r` reloads on demand).

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `task-messaging`: adds a read-only TUI inbox viewer covering both
  `task_messages` and hera role messages for a task.
- `keybindings`: adds `tasklist.inbox` and `hera_rail.inbox`, both default `i`.

## Impact

- `internal/tui/keymap/actions.go` — two actions + labels + context order.
- `internal/tui/modal/inbox.go` (new) — the scrollable read-only viewer widget.
- `internal/tui/app.go` (+ new `inbox.go`) — open/close/load flow, `modeInbox`,
  key routing; tasklist `OnInbox` callback; hera page `OnInbox` callback.
- `internal/tui/taskview/tasklist.go`, `internal/tui/hera/` — dispatch.
- `internal/db/hera_messages.go` — `HeraMessagesForTask`.
- `internal/tui/commandpalette_actions.go` — palette entries (the palette is
  generated from keymap context order; hera rail entry wired to the callback).
- README Reference keybinding table; `help_test.go` assertion;
  `context/knowledge/gotchas/messaging.md` gotcha (viewer must never ack).
- No schema change, no daemon RPC, no REST change.
