## 1. Stop clearing the staged payload on ctrl+y copy

- [x] 1.1 `App.copyStagedClipboard` (`internal/tui/clipboard.go`) copies the
  cached payload and flashes "Copied" but no longer clears
  `a.clipboardPending`/`a.clipboardPendingTask`, no longer calls
  `a.agentHeader.SetClipboardHint(false)`, and no longer calls
  `acc.ClipboardClear(taskID)`. The next tick's `refreshClipboardCache` keeps
  polling and reflects reality (still-staged, replaced, or expired).
- [x] 1.2 `App.copyStagedClipboardForHeraPane` (same file) drops its
  `acc.ClipboardClear(taskID)` call after a successful copy, for the same
  reason.
- [x] 1.3 Update the doc comments on both functions (they currently document
  the clear-after-copy contract) and the `ctrl+y` bullet in
  `context/knowledge/gotchas/keybindings.md` describing the always-intercepted
  behavior.

## 2. Make the staged-payload hint more visible

- [x] 2.1 Add a clipboard-hint color/style constant to
  `internal/tui/theme/theme.go`, following the existing pattern (e.g. the
  `ColorPRAwaiting`/`ColorPRChanges`/`ColorPRApproved` family) — a color
  distinct from existing status colors so it doesn't collide with an
  unrelated meaning (needs-input orange, error red, etc).
- [x] 2.2 `AgentHeader.Draw` (`internal/tui/widget/agentheader.go`) renders the
  " ctrl+y to copy " hint with the new highlight style instead of the current
  plain bold header-text style. Keep the ASCII-only / ` runeWidth` constraint
  documented in the existing comment (ONLY affects style, not the glyph set).
- [x] 2.3 The Hera pane's `(ctrl+y copy)` border-title affordance
  (`internal/tui/hera/page.go`) picks up the same highlight style so the two
  surfaces are visually consistent.

## 3. Tests

- [x] 3.1 `TestCopyStagedClipboard_ClearsLocalStateAndFiresClearRPC` (currently
  in `internal/tui/clipboard_test.go`) is rewritten to assert the OPPOSITE:
  local cached state and the hint stay intact after a copy, and no
  `ClipboardClear` RPC fires. Rename to reflect the new contract (e.g.
  `TestCopyStagedClipboard_PreservesStagedStateAfterCopy`).
- [x] 3.2 Add a test that ctrl+y can be pressed twice in a row with the same
  staged payload and both presses report a successful copy (second press is
  NOT "Nothing to copy").
- [x] 3.3 Update the Hera-pane analogue in `internal/tui/hera/clipboard_test.go`
  the same way — copying does not clear the daemon-side staged payload.
- [x] 3.4 Add/adjust a render test asserting the hint style differs from the
  plain header/border-title text style (color check), for both
  `agentheader.go` and the Hera pane border-title.

## 4. Docs

- [x] 4.1 Update the `ctrl+y` gotcha bullet in
  `context/knowledge/gotchas/keybindings.md` to describe the new
  copy-without-clearing contract and why (TTL/last-write-wins/session-exit
  already own cleanup).
