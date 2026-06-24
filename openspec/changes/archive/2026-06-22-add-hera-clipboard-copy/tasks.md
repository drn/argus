# Tasks

## 1. Resolve the focused pane's task (hera package)

- [ ] 1.1 Add `FocusedTerminalTaskID()` to `panes.go`: `coordBound` when `FocusCoord`, `agentBound` when `FocusAgent && !detailsMode`, else "" (rail/details/remote).
- [ ] 1.2 Tests in `panes_test.go`: returns coord/agent bound id per focus state; "" on rail, details mode, and remote.

## 2. Wire ctrl+y + hint into the page

- [ ] 2.1 Add `OnCopyClipboard func(taskID string)` callback + `clipReady bool` state + `SetClipboardHint(bool)` to `HeraPage`.
- [ ] 2.2 Trap `ctrl+y` in `InputHandler`: when `clipReady && terminalPaneFocused() && OnCopyClipboard != nil` and `FocusedTerminalTaskID() != ""`, fire the callback and consume; otherwise fall through to the per-region dispatch (PTY).
- [ ] 2.3 Render the `(ctrl+y copy)` affordance in `Draw`: set the focused terminal pane's border title via a `clipboardHintTitle` helper when `clipReady`.
- [ ] 2.4 Tests in `page_test.go`: ctrl+y fires `OnCopyClipboard` with the focused pane's task only when staged; falls through when not staged; inert on rail/details; border-title hint smoke.

## 3. Copy path (App)

- [ ] 3.1 Add `copyStagedClipboardForHeraPane(taskID)` to `clipboard.go`: direct `ClipboardGet` lookup → `copyToClipboard("Copied")` → async `ClipboardClear`; logged no-op when not daemon-backed or nothing staged. uxlog prefix `[hera]`.
- [ ] 3.2 Add `refreshHeraClipboardHint()` to `app.go`: poll `ClipboardGet` for `FocusedTerminalTaskID()` (single task) and `SetClipboardHint(present && text != "")`; off when no terminal focus / not daemon-backed.
- [ ] 3.3 Wire `heraPage.OnCopyClipboard = a.copyStagedClipboardForHeraPane` (local-mode block) and call `refreshHeraClipboardHint()` in the Hera-tab tick block.
- [ ] 3.4 Tests in `clipboard_test.go`: copy with a fake daemon-backed runner; no-op paths logged.

## 4. Docs (mandatory, same PR)

- [ ] 4.1 Help overlay: add `{"ctrl+y", "copy staged text (focused pane)"}` to the "Hera View (rail)" section in `modal/help.go`; assert it in `help_test.go` (`TestHelpModal_Draw`).
- [ ] 4.2 README Reference appendix: add `ctrl+y` to the Hera rail keybinding table.
- [ ] 4.3 `context/knowledge/gotchas/hera-view.md`: document focused-pane scoping + the conditional PTY fall-through gate.

## 5. Validate

- [ ] 5.1 `make pre-pr` passes clean (build + vet + fmt-check + lint-pr + vuln + test-cover-gate ≥88%).
