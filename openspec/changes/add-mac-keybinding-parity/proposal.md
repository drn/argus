## Why

The macOS companion app has only four keyboard shortcuts today (new task, rename, schedules, quit), so a user who wants to work entirely from the mac app — never opening the TUI — has no way to perform most task/rail/terminal actions without reaching for the mouse. The TUI's keymap already defines the full set of useful actions; this change ports the ones that make sense in a GUI into the mac app, either as a keyboard shortcut or a button/menu affordance, so every action the TUI supports is reachable in the mac app too.

## What Changes

- Add mac-native keyboard shortcuts for: tab switching, help, destroy, fork, open-repo, open-PR, jump-to-next-needs-input (global chrome).
- Add a "Prune stale worktrees" item to the toolbar overflow menu.
- Add a right-click context menu on task rail rows for status advance/revert.
- Add keyboard shortcuts to the existing archive and pin actions.
- Add a Cmd+F shortcut that focuses the sidebar's filter field, and a visible toggle in the filter bar for showing/hiding hera-managed tasks.
- Add Cmd+↑/↓ (task switch), Cmd+←/→ (pane focus), and Shift+↑/↓/PageUp/PageDown/End (scrollback) handling in the Terminal tab, intercepted before reaching SwiftTerm so they never leak into the PTY stream.
- Add a keyboard shortcut for copying the terminal's visible output.
- Add a toolbar button in the Terminal tab that opens a "Switch Claude session" picker sheet for the current task.
- Explicitly defer (named follow-ups, not silently dropped): a global command palette, a global task/role switcher beyond direct sidebar selection, restore-rail, copy branch/path, manual refresh and the agent-view "show links" affordance, Files-tab per-row actions, and all Hera-rail mutation keys (relocated to the separate Hera-rail-toggle change).

## Capabilities

### New Capabilities

(none — this change extends the existing `macos-app` capability rather than introducing a new one)

### Modified Capabilities

- `macos-app`: adds keyboard-shortcut, context-menu, and toolbar requirements across the app shell/rail (existing "App shell & task rail" requirement) and the terminal surface (existing "Live terminal streaming" requirement), plus a new requirement for the terminal-safe key-interception mechanism and the deferred-scope statement.
- `rest-api`: adds two new endpoints (list / switch a task's Claude sessions) so the mac app can reach `internal/claudesession`, which previously had no REST route (see design.md D3).

## Impact

- `macos/Sources/Argus/Sidebar.swift`, `TaskRow.swift`, `TaskActions.swift` — context menu, filter field, hera-managed toggle, archive/pin shortcuts.
- `macos/Sources/Argus/ContentView.swift`, `ArgusApp.swift` — new `.commands`/menu items for global chrome shortcuts and the overflow-menu prune item.
- `macos/Sources/Argus/TerminalTab.swift`, `TerminalController.swift` — extended local `NSEvent` monitor for terminal-safe chords; new toolbar button + session-picker sheet.
- `internal/api/routes.go`, `internal/api/handlers.go` (or a dedicated new handler file) — two new minimal REST endpoints (`GET`/`POST /api/tasks/{id}/claude-session(s)`) so the mac app can reach `internal/claudesession`, which today is only called in-process by the TUI. Every other action in this change already exists as a daemon endpoint; this one exception is called out in `design.md` D3 and delta-specced under `specs/rest-api/spec.md`. The web SPA's own use of these new endpoints is an explicit follow-up, not implemented in this change (see design.md Non-Goals).
