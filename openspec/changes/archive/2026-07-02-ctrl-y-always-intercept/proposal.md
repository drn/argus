## Why

`ctrl+y` (copy agent-staged clipboard) has always been a *conditional*
intercept: Argus only steals the key from the PTY when a payload is actually
staged for the focused task. With nothing staged, the raw control byte (0x19)
passes straight through to whatever CLI is running in the pane. That CLI often
has its own emacs-style "yank" bound to Ctrl-Y, which pastes back whatever is
sitting in ITS OWN kill-ring (populated by Ctrl-U/Ctrl-K/Ctrl-W). In practice
this means a stale, unrelated string — e.g. a screenshot file path that was
typed/dragged into the prompt and then cleared — can reappear in the terminal
unpredictably whenever the user presses ctrl+y with nothing staged. The
fallthrough was a deliberate trade-off (preserve in-agent vim/emacs yank), but
it produces confusing, hard-to-explain behavior for users who don't know
Argus's clipboard-staging feature exists.

## What Changes

- **`ctrl+y` is now unconditionally intercepted** in both the main agent view
  and the native Hera view. It never reaches the PTY.
  - If a payload is staged for the relevant task, behavior is unchanged: copy
    to the OS clipboard, clear the staged state, flash "Copied".
  - If nothing is staged, Argus now flashes a "Nothing to copy" header notice
    instead of forwarding the raw byte to the underlying CLI.
- **Trade-off accepted:** in-agent vim/emacs `yank`/`yank-pop` on ctrl+y no
  longer works inside an Argus pane. This is intentional — the prior
  fallthrough's silent, stale-kill-ring pastes were confusing enough (see the
  motivating bug report) that predictable clipboard-copy semantics win.
  Rail / coordinator-details focus in the Hera view still has no PTY to
  intercept from, so ctrl+y remains an inert no-op there (unchanged).

## Capabilities

### Modified Capabilities

- `clipboard-staging`: the TUI copy action no longer reports "nothing to copy,
  fall through" — it always consumes the key and surfaces a notice either way.
- `hera-view`: the focused-pane ctrl+y copy binding is no longer gated on a
  staged payload — it always fires and never falls through to the PTY.

## Impact

- **Modified code:**
  - `internal/tui/clipboard.go` — new `flashNotice` helper; `copyStagedClipboard`
    doc updated; `copyStagedClipboardForHeraPane` flashes "Nothing to copy" on
    the no-payload / non-daemon-backed paths instead of a silent log-only no-op.
  - `internal/tui/app.go` — `ActAgentCopy` dispatch always returns `nil`
    (never falls through), flashing the notice when `copyStagedClipboard`
    reports false.
  - `internal/tui/hera/page.go` — `ctrl+y` trap no longer gates on `clipReady`;
    fires `OnCopyClipboard` (and returns, consuming the key) whenever a
    terminal pane is focused, regardless of staged state. `clipReady` now only
    drives the `(ctrl+y copy)` border-title hint.
  - Tests updated in `internal/tui/clipboard_test.go` and
    `internal/tui/hera/clipboard_test.go` to assert always-intercept + notice
    behavior instead of PTY fallthrough.
- **No schema change, no new daemon RPC, no new dependency.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring changes. The quality gate stays `make pre-pr`.
