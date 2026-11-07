## 1. Regression test

- [x] 1.1 Add `TestScreenRenderer_ScrollbackStaysBounded` (fails before fix: 177 -> 9027 lines)
- [x] 1.2 Add equivalent bounded-scrollback tests for usagebudget and codexprobe renderers

## 2. Fix

- [x] 2.1 `ScreenRenderer`: `SetScrollbackSize(1)` at creation, `ClearScrollback()` after RIS
- [x] 2.2 Same for usagebudget and backendtier probe emulators
- [x] 2.3 uxlog creation/reset counts

## 3. Docs and ship

- [x] 3.1 Gotcha in `context/knowledge/gotchas/pty-terminal.md` and index row
- [x] 3.2 `make pre-pr`, hera-review + test-adversary, archive change in the PR
