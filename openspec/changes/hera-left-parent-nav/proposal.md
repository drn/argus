## Why

When the Hera rail is focused, there is no way to jump directly from a worker (or any nested row) back up to its parent coordinator without pressing `k` repeatedly. For deep subtrees this is tedious. A dedicated `←` binding that navigates to the nearest parent coordinator row closes the gap.

The Left arrow was deliberately left unbound in the rail to avoid the BUG-001 class of accidental PTY injection. This change scopes the new binding exclusively to `FocusRail` — when a content pane is focused, Left still passes through to the PTY unchanged.

## What Changes

- Add `Rail.CursorToParent()` method: walks the flattened row list backwards from the cursor, lands on the nearest ancestor with strictly smaller depth whose kind is `rrOrch` or bridging `rrRole` (collOrchID > 0).
- Add `KeyLeft` intercept in `HeraPage.InputHandler` that calls `CursorToParent()` when `focus.State() == FocusRail` and returns; non-rail Left falls through to `forwardKey` (PTY pass-through unchanged).
- Add the new binding to the help overlay (`internal/tui/modal/help.go` Hera section) and the README Reference keybinding table.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: rail navigation gained a new key — Left moves the cursor to the parent coordinator row when the rail is focused.

## Impact

- `internal/tui/hera/rail.go` — `CursorToParent()` method
- `internal/tui/hera/page.go` — InputHandler `KeyLeft` branch (FocusRail only)
- `internal/tui/modal/help.go` — HelpSections Hera rail entry
- `README.md` — Hera Tab keybinding table
