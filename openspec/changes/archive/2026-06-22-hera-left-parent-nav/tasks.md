## 1. Implementation

- [x] 1.1 Add `Rail.CursorToParent()` in `internal/tui/hera/rail.go`
- [x] 1.2 Write failing test `TestRail_CursorToParent` (5 subtests)
- [x] 1.3 Add `KeyLeft` intercept in `HeraPage.InputHandler` (FocusRail guard)
- [x] 1.4 Write failing tests `TestHeraPage_LeftArrowMovesSelectionToParentOnRailFocus` and `TestHeraPage_LeftArrowFromPaneDoesNotMoveRail`
- [x] 1.5 Confirm all tests green after implementation

## 2. Documentation & Discoverability

- [x] 2.1 Add `←` entry to `HelpSections` Hera rail section in `internal/tui/modal/help.go`
- [x] 2.2 Add assertion for new entry in `help_test.go`
- [x] 2.3 Add row to README Reference Hera Tab keybinding table
- [x] 2.4 Add gotcha in `context/knowledge/gotchas/hera-view.md`

## 3. CI

- [x] 3.1 `make pre-pr` clean
