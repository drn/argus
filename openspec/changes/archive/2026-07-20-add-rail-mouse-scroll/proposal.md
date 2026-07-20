## Why

Every list-shaped panel in the TUI that already supports a mouse (`gitpanel.FilePanel`, `terminalpane`/`terminal` panes, `dagview`, `modal.HelpModal`, `SettingsView`) lets the operator scroll it with the wheel. The two primary navigation rails — the Tasks tab's `TaskListView` and the Hera/Projects tab's `Rail` — had no `MouseHandler` at all, so they silently inherited `tview.Box`'s default (which only handles `MouseLeftDown` for focus). A wheel notch over either rail was swallowed unconsumed and did nothing, forcing keyboard-only navigation on the two most-used panels in the app.

## What Changes

- Add a `MouseHandler` to `taskview.TaskListView`: `MouseLeftDown` focuses the list (matching the prior `Box` default so nothing regresses); `MouseScrollUp`/`MouseScrollDown` call the existing `CursorUp`/`CursorDown` (the same methods `k`/`j`/arrow keys already use), so a wheel notch moves the cursor exactly one row, skipping headers/separators identically to keyboard nav.
- Add the same `MouseHandler` shape to `hera.Rail`, routing wheel scroll to its existing `CursorUp`/`CursorDown` (→ `step()`), so it participates in the same selectable-row skipping, kanban-group boundary crossing, and cursor-persistence (`setCursor`'s `persist()`) that keyboard nav already exercises.
- Both handlers gate on `InRect` before acting, since each widget is one item among several forwarded every mouse event by tview's `Flex`/region-dispatch machinery (`TaskPage`'s inner `Flex`, `HeraPage.MouseHandler`'s `regionAt`-gated dispatch).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `task-list-view`: adds a requirement that the task list responds to mouse wheel scroll by moving the cursor, mirroring keyboard up/down navigation.
- `hera-view`: adds a requirement that the rail responds to mouse wheel scroll by moving the cursor, mirroring keyboard up/down navigation.

## Impact

- `internal/tui/taskview/tasklist.go`: new `MouseHandler` method.
- `internal/tui/taskview/tasklist_test.go`: new tests for scroll, left-click focus, and out-of-rect no-op.
- `internal/tui/hera/rail.go`: new `MouseHandler` method.
- `internal/tui/hera/rail_test.go`: new tests for scroll, left-click focus, and out-of-rect no-op.
- `internal/tui/hera/panes_cover_test.go`: updated a comment that had documented the rail's prior lack of a `MouseHandler` (now stale) — the test itself (pinning that a wheel over the agent/coord region routes to that pane, not the rail) is unchanged.
- `context/knowledge/gotchas/hera-view.md`, `context/knowledge/gotchas/tasklist-ui.md`: gotcha bullets documenting the new handlers.
- No REST API, MCP, web, or macOS client changes — this is TUI-only (mouse input is a terminal-local concept; there is no equivalent surface on the other two frontends to keep in parity for a scroll-wheel gesture).
