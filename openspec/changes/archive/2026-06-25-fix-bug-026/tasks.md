# Tasks

- [x] 1.1 Reproduce the mechanism: confirm a full-screen (alt-screen) agent that redraws in place yields ~0 terminal scrollback (`sbLen=0`, `maxScroll=0`), via a unit test and by feeding real session logs through the replay emulator.
- [x] 1.2 Failing test: `TestTerminalPane_WheelForwardsToAltScreenAgent` — wheel over an alt-screen live session forwards an SGR frame and does not advance the pane scroll offset.
- [x] 2.1 `agentOwnsWheel()` — live session AND `emu.IsAltScreen()`.
- [x] 2.2 `forwardWheel(cb, event)` — encode `ESC[<cb;Cx;Cy M` (1-based inner-rect coords, clamped) and write via `sess.WriteInput`.
- [x] 2.3 Wire both wheel cases in `TerminalPane.MouseHandler` (diff mode → diff scroll; alt-screen agent → forward; else → terminal scrollback).
- [x] 3.1 Regression test: `TestTerminalPane_WheelScrollsScrollbackWhenNotAltScreen` — non-alt-screen wheel scrolls local scrollback and forwards nothing.
- [x] 4.1 Targeted suites green under `-race` (`./internal/tui/terminal/... ./internal/tui/hera/... ./internal/tui/terminalpane/...`); `go vet` + `gofmt` clean.
- [x] 5.1 Gotcha note in `context/knowledge/gotchas/pty-terminal.md`.
- [ ] 6.1 Live dogfood: rebuild + relaunch argus, confirm wheel-up scrolls a full-screen agent's view in the Hera coord/agent panes and the Tasks-tab agent view (coordinator/Aaron — TUI renders client-side, needs relaunch).
