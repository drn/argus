# Tasks: hera-focus-hint-bar

- [x] Add `heraFocus int` field and `SetHeraFocus(int)` / `HeraFocus() int` to `internal/tui/widget/statusbar.go`
- [x] Update `StatusBar.Draw()` TabHera case to render focus-aware hint sets (rail vs pane)
- [x] Add `OnFocusChange func(Focus)` callback and `notifyFocusChange()` helper to `internal/tui/hera/page.go`
- [x] Defer `notifyFocusChange()` in `HeraPage.InputHandler` to fire on every exit (Tab, Ctrl+Q, Enter, etc.)
- [x] Call `notifyFocusChange()` in `HeraPage.MouseHandler` on click events
- [x] Wire `OnFocusChange` in `internal/tui/app.go` to `statusbar.SetHeraFocus(int(f))`
- [x] Reset `heraFocus` to 0 in `switchToHeraTab2` so each tab entry starts with rail hints
- [x] Add unit tests to `internal/tui/widget/statusbar_test.go` (rail hints, pane hints, agent hints, default)
- [x] Add unit tests to `internal/tui/hera/page_test.go` (OnFocusChange fires on Tab + Ctrl+Q)
- [x] Add smoke test `TestSmoke_HeraFocusChangesStatusBarHints` to `internal/tui/smoke_test.go`
- [x] Add gotcha entry to `context/knowledge/gotchas/hera-view.md`
- [x] OpenSpec delta under `openspec/changes/hera-focus-hint-bar/specs/hera-view/spec.md`
- [x] `make pre-pr` green (stdlib-only vuln pre-existing)
