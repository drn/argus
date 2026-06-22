## Why

Cmd+Up / Cmd+Down (mod-7 encoded as `\x1b[1;7A`/`\x1b[1;7B`) should move the Hera rail selection while the user stays focused in a content pane, matching the agent-view pattern where Cmd+Up/Down navigates the task list. Without this the mod-7 sequence falls through to the pane PTY — confusing the agent — and the user must leave the pane to move the rail cursor.

## What Changes

- Add `KeyUp` / `KeyDown` + `ModCtrl|ModAlt` intercept in `HeraPage.InputHandler` that calls `rail.CursorUp()` / `rail.CursorDown()` and returns without forwarding to the focused pane.
- Add the new binding to the help overlay (`internal/tui/modal/help.go` Hera section) and the README Reference keybinding table.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: rail navigation gained a new key chord — Cmd+Up/Down moves rail selection from any focus state.

## Impact

- `internal/tui/hera/page.go` — InputHandler top-level key switch
- `internal/tui/modal/help.go` — HelpSections Hera rail entry
- `README.md` — Hera Tab keybinding table
