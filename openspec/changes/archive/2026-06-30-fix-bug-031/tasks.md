## 1. Guard scroll-mode entry for alt-screen panes

- [x] 1.1 Add `TerminalPane.InAltScreen()` (`tp.emu != nil && tp.emu.IsAltScreen()`)
  as the single alt-screen signal; refactor `agentOwnsWheel` to reuse it.
- [x] 1.2 Early-return from `ScrollUp` and `AccelScrollUp` when `InAltScreen()` so
  `scrollOffset` is never raised for a full-screen agent (no `[SCROLL]` entry).

## 2. Suppress keyboard scroll + show affordance

- [x] 2.1 Main agent view (`App.handleAgentKey`): on `Shift+↑` / `Shift+PgUp`
  when the pane is alt-screen, set a transient status-bar notice and return
  instead of scrolling.
- [x] 2.2 Hera pane (`HeraPage.forwardKey`): on `PgUp` when the pane is
  alt-screen, fire the new `OnInfo` affordance callback and return; wire
  `OnInfo` to the status bar in `App`.

## 3. Tests

- [x] 3.1 Terminal: alt-screen pane + `ScrollUp`/`AccelScrollUp` keeps
  `scrollOffset` at 0; non-alt-screen pane scrolls as before; leaving alt-screen
  (`ESC[?1049l`) resumes normal scrollback.
- [x] 3.2 Hera: `forwardKey` `PgUp` on an alt-screen worker pane suppresses scroll
  and fires `OnInfo`; a non-alt-screen pane still scrolls.
- [x] 3.3 Main view smoke: `Shift+↑` / `Shift+PgUp` on an alt-screen agent pane
  suppresses scroll and sets the status-bar affordance.

## 4. Docs

- [x] 4.1 Extend `context/knowledge/gotchas/pty-terminal.md` (#67 area) noting the
  keyboard/scroll-mode-entry suppression for alt-screen panes; bump the index
  count.
