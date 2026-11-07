## Why

`cap-output-repaint-rate` (#1120) gives a keystroke or paste echo the 8ms repaint cap and everything else the 40ms output cap. A mouse click forwarded to a live agent (add-click-passthrough, e.g. Claude Code's "Jump to bottom (click)") also makes the agent redraw, but it was left out and painted at 40ms. The coordinator asked for it in msg #21699 (item 2); it was missed in that PR.

## What changes

- A click press or release forwarded to a live session reports user input through the existing `TerminalPane.OnUserInput` hook, opening the 150ms echo window.
- Wheel scrolling is not reported: it is not an echo.
- Clicks that are not forwarded (unfocused pane, no live session) report nothing.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: forwarded clicks open the echo window.

## Impact

- **Code:** `internal/tui/terminal/terminalpane.go` (a `reportUserInput` helper called from the click press and release forwarders; the paste path now uses it too).
- **Unchanged:** supervisor `ProtocolVersion`, `TestSupervisorSurfaceDigest`, wire formats, REST surface, no `screen.Sync()`.
- **Deployment:** a TUI relaunch only.
- **Frontend parity:** TUI-internal repaint timing; web and macOS are unaffected.
