# Fix BUG-008: snap terminal scroll to bottom on user input

## Why

When the agent terminal pane is scrolled up into history (`scrollOffset > 0`) and
the user types, their input is sent to the live PTY but the viewport stays pinned
in the scrollback by anchor-lock — so the keystrokes (and the cursor) echo at the
live bottom, which is off-screen. The user sees nothing happen and assumes input
was dropped.

The task page agent view already snaps to the live tail before forwarding a key
(`handleAgentKey` calls `ResetScroll()` on any non-scroll key), but two other
input paths into the same shared `TerminalPane` widget do not:

- Hera panes (`HeraPage.forwardKey`) write encoded keystrokes straight to the
  session without resetting scroll.
- The pane's own `PasteHandler` writes bracketed paste to the session without
  resetting scroll.

## What Changes

- Real user input written to the live PTY (printable keys, Enter, control chars,
  paste) SHALL snap the pane back to the live tail (`scrollOffset = 0`) so the
  input and cursor are immediately visible.
- Scrollback-navigation keys (PgUp / PgDn / Shift+arrows / Home / End) SHALL NOT
  snap — they continue to browse history.
- New agent OUTPUT arriving while scrolled up SHALL continue to keep the viewed
  content pinned (anchor-lock unchanged) — only INPUT snaps.

## Impact

- Affected specs: `terminal-rendering` (new requirement: snap-to-bottom on input)
- Affected code: `internal/tui/hera/panes.go` (`forwardKey`), `internal/tui/terminal/terminalpane.go` (`PasteHandler`)
- No change to the task page agent view, which already snaps.
