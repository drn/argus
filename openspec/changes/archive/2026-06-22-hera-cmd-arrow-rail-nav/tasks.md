## 1. Implementation

- [x] 1.1 Add `KeyUp`/`KeyDown` + `ModCtrl|ModAlt` intercept in `HeraPage.InputHandler` calling `rail.CursorUp()`/`rail.CursorDown()`
- [x] 1.2 Write failing test `TestHeraPage_CmdArrowMovesRailSelectionWithoutChangingFocus`
- [x] 1.3 Confirm test green after implementation

## 2. Documentation & Discoverability

- [x] 2.1 Add `Cmd+↑ / Cmd+↓` entry to `HelpSections` Hera rail section in `internal/tui/modal/help.go`
- [x] 2.2 Add assertion for new entry in `help_test.go`
- [x] 2.3 Add row to README Reference Hera Tab keybinding table
- [x] 2.4 Add gotcha in `context/knowledge/gotchas/hera-view.md`

## 3. CI

- [ ] 3.1 `make pre-pr` clean
