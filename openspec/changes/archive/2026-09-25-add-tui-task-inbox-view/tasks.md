## 1. Data

- [x] 1.1 `db.HeraMessagesForTask(taskID, limit)` — all read states, oldest first; tests (no bindings, read+unread incl. ended binding, other-task exclusion, limit).

## 2. Keymap

- [x] 2.1 Add `ActTaskInbox` / `ActHeraInbox` (default `i`) + labels + contextOrder.
- [x] 2.2 `help_test.go` assertion; README Reference keybinding table.

## 3. Modal widget

- [x] 3.1 `modal.InboxModal`: entries, loading/error/empty/remote states, wrapped bodies, scroll (j/k/arrows/PgUp/PgDn/g/G), `r` reload request, Esc/q/i close; opens at bottom.
- [x] 3.2 Render + key tests.

## 4. App wiring

- [x] 4.1 `modeInbox`, open/close, key routing, off-thread load merging both stores, sender name resolution, uxlog `[inbox]`.
- [x] 4.2 Tasklist `OnInbox` dispatch; Hera rail dispatch → selected role's task.
- [x] 4.3 Command palette entries.
- [x] 4.4 Tests: open from tasklist, open from rail, never acks (unread stays unread), remote-mode placeholder, smoke test open/close.

## 5. Docs & close-out

- [x] 5.1 Gotcha in `context/knowledge/gotchas/messaging.md` (viewer must never ack; hera inbox tool is unread-only).
- [x] 5.2 `make pre-pr` green.
- [x] 5.3 Archive the change into base specs before merge.
