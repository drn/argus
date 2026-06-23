# Tasks

- [x] 1. Add `CopyChoiceModal` to `internal/tui/modal/` (Name/Prompt choices, nav, outcome reporting) + unit tests.
- [x] 2. Rename `TaskListView.OnCopyPrompt` → `OnCopy`; fire on any selected task (drop empty-prompt gate). Update tasklist tests.
- [x] 3. Wire the modal into `app.go`: new `modeCopyChoice`, open/handle/close, copy name or prompt via `copyToClipboard`. Dispatch in input handler.
- [x] 4. Update help modal (`modal/help.go`) + `help_test.go` for the new `c` action.
- [x] 5. Update README reference keybinding table.
- [x] 6. Add a smoke test exercising `c` → modal open → select.
- [x] 7. Document any non-obvious gotcha; `make pre-pr` green.
- [x] 8. Archive this change folder within the PR.
