# Fix BUG-026: forward the mouse wheel to full-screen agents

## Why

In the native Hera coordinator/agent panes (and the Tasks-tab agent view, which share `TerminalPane`), the mouse wheel-up did nothing for many sessions, while it worked for others. The reported "can't scroll new/active sessions, can scroll old/settled ones" framing pointed at routing, but the real cause is rendering-mode dependent:

- Agents like Claude Code / Codex run as **full-screen TUIs**: they take the alternate screen (`ESC[?1049h`) and redraw in place with cursor-home (`ESC[H`) instead of emitting scrolling output. That produces little or no terminal **scrollback**, so the pane's wheel-scroll (which scrolls its own scrollback) clamps to the bottom — wheel-up is a silent no-op.
- This was confirmed by feeding two real session logs through the replay emulator: a coordinator that emitted scrolling output yielded ~2791 scrollback lines (scrollable); a worker that redrew in place yielded 0 (`maxScroll`=0, not scrollable). Both were in the alternate screen, so alt-screen presence alone is not the discriminator — committed scrollback depth is.
- It is NOT a routing or click-to-focus regression: the wheel reaches the pane (scroll-DOWN visibly redrew the cursor); there was simply nothing in scrollback to reveal.

A real terminal hands the wheel to the foreground full-screen app (which has its own scroll), rather than scrolling the terminal's scrollback. Argus already does this for plugin views (#681) but not for the native agent/coordinator panes.

## What Changes

- When the live agent has grabbed the screen as a full-screen app (alternate screen — the signal that it wants the wheel), `TerminalPane.MouseHandler` FORWARDS wheel events to the agent as SGR mouse frames (`ESC[<64|65;Cx;Cy M`) via the session input, so the agent scrolls its own view.
- When the agent is not full-screen (main screen) or the session is finished/replay, the pane keeps its existing terminal-scrollback scroll behavior unchanged.
- No keybinding changes (mouse-only); the help modal and README key tables are unaffected.

## Impact

- Affected specs: `terminal-rendering` (adds a wheel-forwarding requirement).
- Affected code: `internal/tui/terminal/terminalpane.go` (`MouseHandler`, new `agentOwnsWheel`/`forwardWheel`). Fixes both the Hera panes and the Tasks-tab agent view at once (shared widget).
