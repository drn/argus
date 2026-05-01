## Key Bindings & Navigation

- **`ctrl+c` only exits from task list view.** In agent mode, writes `0x03` to PTY (or no-op if dead).
- **`ctrl+q` in diff mode must exit diff AND refocus terminal.** Otherwise user needs a second keypress.
- **`ctrl+d` exits agent view when session is dead.** Without this, Ctrl+D after agent exit is silently dropped.
- **`ctrl+p` opens PR URL (works while agent runs).** `o` also works when session is finished.
- **`ctrl+l` opens fuzzy link picker (works while agent runs).** Uses `tcell.KeyCtrlL` — reliable across all terminals (unlike `ctrl+/` which has inconsistent encodings). Overrides the typical "clear screen" behavior; the agent PTY never sees it. Reads session log in a background goroutine; the `QueueUpdateDraw` callback must guard `a.mode == modeAgent` because the user may leave agent view during I/O.
- **Escape in agent view:** Refocuses terminal from diff/files but does NOT exit agent view. Always returns `nil` to consume the event.
- **Mouse clicks must update `agentFocus`, not just tview focus.** Custom `MouseHandler` overrides needed.
- **In diff mode: Up/Down switch files, j/k scroll diff.**
- **Cmd+Up/Down navigates between tasks in agent view** via `ModCtrl|ModAlt` check.
- **tcell has no `KeyCtrlLeft`/`KeyCtrlRight`.** Check `event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0`.
- **`exitAgentView` must reset `header.SetTab(TabTasks)` and `statusbar.SetTab(TabTasks)`.** The global key handler routes non-agent keys based on `header.ActiveTab()` — if the tab isn't reset, up/down keys get routed to the wrong tab's handler (e.g., Reviews) instead of the task list.
- **Settings `d` key is context-dependent: project rows → delete (with confirmation), backend rows → set default.** `handleDeleteOrDefault` dispatches on `currentSection()`. Deleting a project orphans its tasks (no FK constraint) — the confirmation modal counts tasks via `a.tasks` and warns the user.
- **`ctrl+y` in agent view is conditionally intercepted — only steals from the PTY when an agent has staged a clipboard payload.** With nothing staged, `copyStagedClipboard()` returns false and the switch falls through to the PTY pass-through path so vim/emacs `yank-pop` still reaches the underlying program. The intercept fires synchronously on the tview goroutine; the actual pbcopy and clear-RPC run in goroutines.
