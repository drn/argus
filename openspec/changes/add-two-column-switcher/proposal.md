## Why

Bouncing between argus tasks and Hera projects takes enough keystrokes that the user opens a plain terminal tab and runs vanilla `claude` instead of using a task. A single hotkey that works from anywhere and shows both rails side by side makes jumping into a task faster than opening a new tab.

## What Changes

- Add a two-column jump popup: the Tasks list (left, hera-managed tasks always hidden) and the Projects rail (right), each rendered by a private copy of the real widget.
- Arrow keys move within (↑/↓) and between (←/→) columns; typing filters both columns immediately; `ctrl+g` moves the cursor to the next `(?)`; Enter goes there; Esc clears the filter, or closes the popup when no filter is applied.
- The cursor opens on the first `(?)` (Tasks column first, then Projects), else on the currently-viewed task.
- **BREAKING**: `ctrl+j` now opens the popup via a new unconditional global action `global.switcher`, replacing the `agent.switcher` agent-context action; config overrides of `agent.switcher` no longer apply.
- Remove the old single-list grouped task/role switcher and its three separate dispatch paths (agent view, Tasks tab block, Hera page literal case). The flat `TaskSwitcherModal` mode stays for the Hera parent picker.
- TUI-only. Named follow-up: a cross-project jump popup for the web SPA and macOS app.

## Capabilities

### New Capabilities

- `jump-switcher`: the two-column Tasks/Projects jump popup — reach, layout, navigation, filtering, needs-input cycling, start position, and Enter/Esc behavior.

### Modified Capabilities

- `keybindings`: the `ctrl+j` switcher requirement changes from the agent-context `agent.switcher` (plus a Tasks-tab special case) to the unconditional global `global.switcher`.
- `hera-view`: the old unified-switcher requirements (reachable from Hera, needs-input-first ordering, ancestor expansion on select) are replaced — Hera reach now comes from the global dispatch, ordering from the popup's needs-input cursor, and ancestor expansion from the Projects-column Enter.
- `forms-and-modals`: the task switcher status-indicator requirement drops its grouped (Ctrl+J) scenario, since grouped mode is removed.
- `command-palette`: the classic agent view's palette keeps listing the switcher after it moves from the agent context to the global context.

## Impact

- `internal/tui/keymap/actions.go` — `ActAgentSwitcher` removed, `ActGlobalSwitcher` added (defaults, labels, context order).
- `internal/tui/app.go` — global dispatch case, removal of the Tasks-tab block and `handleAgentKey` case, popup open/close/key handling and Enter routing; `openTaskSwitcher` removed.
- `internal/tui/switcherpopup.go` (new) — the popup widget.
- `internal/tui/taskview/tasklist.go` — `SetFilter`, `NextNeedsInputTaskID` seams.
- `internal/tui/hera/rail.go`, `internal/tui/hera/page.go` — `SetFilter`, `CopyFoldStateFrom`, `SelectOrch` seams; `OnSwitcher` and the `ctrl+j` literal case removed.
- `internal/tui/taskswitcher.go` — grouped mode removed.
- `internal/tui/commandpalette_actions.go`, help tests, README Reference keybinding table, `context/knowledge/gotchas/keybindings.md`.
- No REST, daemon, schema, or macOS/web changes.
