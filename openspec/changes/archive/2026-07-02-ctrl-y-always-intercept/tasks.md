## 1. Main agent view

- [x] 1.1 `internal/tui/clipboard.go` — add `flashNotice(notice string)`
  (sets the header notice synchronously, auto-clears after 2s via
  `QueueUpdateDraw`, mirroring `copyToClipboard`'s clear pattern).
- [x] 1.2 `internal/tui/app.go` — `ActAgentCopy` case always `return nil`;
  call `a.flashNotice("Nothing to copy")` when `copyStagedClipboard()` is false.

## 2. Hera view

- [x] 2.1 `internal/tui/hera/page.go` — drop the `p.clipReady` gate from the
  `ctrl+y` trap; fire `OnCopyClipboard(id)` and `return` whenever a terminal
  pane is focused, regardless of staged state. `clipReady` stays wired to the
  `(ctrl+y copy)` border-title hint only.
- [x] 2.2 `internal/tui/clipboard.go` — `copyStagedClipboardForHeraPane` calls
  `a.flashNotice("Nothing to copy")` on the not-daemon-backed and
  nothing-staged paths (previously a silent logged no-op).

## 3. Tests

- [x] 3.1 `internal/tui/hera/clipboard_test.go` —
  `TestCtrlY_FallsThroughWhenNotStaged` rewritten to
  `TestCtrlY_AlwaysInterceptsWhenNotStaged`: asserts `OnCopyClipboard` fires
  with the focused pane's task even when nothing is staged, and the PTY never
  receives the byte.
- [x] 3.2 `internal/tui/clipboard_test.go` — new
  `TestActAgentCopy_NothingStaged_FlashesNotice`; `TestCopyStagedClipboardForHeraPane_NoTaskOrAccessor`
  and `_AbsentNoCopy` extended to assert the "Nothing to copy" notice.

## 4. Docs

- [x] 4.1 Update stale doc comments describing the old conditional-intercept /
  PTY-fallthrough behavior in `internal/tui/app.go` and
  `internal/tui/hera/page.go`.
- [x] 4.2 `context/knowledge/gotchas/keybindings.md` and
  `context/knowledge/gotchas/hera-view.md` — replace the conditional-intercept
  bullets with the always-intercept behavior.
- [x] 4.3 `README.md` — Reference appendix ctrl+y rows updated to drop the
  "falls through to PTY" / "in-agent yank still works" language.
