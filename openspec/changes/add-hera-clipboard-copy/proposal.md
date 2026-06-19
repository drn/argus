## Why

The `ctrl+y` "copy agent-staged clipboard" keybinding works in the main agent
view but was never wired into the native Hera view. An agent running under a
Hera coordinator or worker can call `argus_clipboard_set` to stage a payload
daemon-side, but in a Hera coordinator/worker pane nothing in the TUI reads it:
`ctrl+y` does nothing (it just falls through to the PTY). The staging half of
the feature works for Hera-bound agents; the copy half is missing for the Hera
view.

Unlike the main agent view there is no single "active" task in the Hera view —
it shows several tasks at once (a coordinator pane plus a worker/agent pane).
So the copy must be scoped to whichever pane currently holds focus, resolving
that pane's bound argus task and copying THAT task's staged payload.

## What Changes

- **Wire `ctrl+y` into the native Hera view.** When a coordinator pane or a
  worker terminal is focused and that pane's task has an agent-staged clipboard
  payload, `ctrl+y` copies the payload to the OS clipboard (and clears the
  daemon-side slot), exactly like the main agent view. The payload is resolved
  from the FOCUSED pane's bound task — not a global active task. When nothing is
  staged, `ctrl+y` falls through to the PTY so vim/emacs-style yank still
  reaches the agent (same conditional-intercept semantics as the agent view).
  Rail / coordinator-details focus has no terminal and nothing to copy, so
  `ctrl+y` is an inert no-op there.
- **Surface a discoverability hint.** When the focused terminal pane's task has
  a staged payload, the pane's border title gains a `(ctrl+y copy)` affordance —
  the Hera-view analogue of the main agent view's header hint, consistent with
  how the Hera view already labels panes via `SetBorderTitle`.
- **Reuse the existing daemon-backed accessor.** The copy path goes through the
  same `clipboardAccessor` (`ClipboardGet`/`ClipboardClear`) and `copyToClipboard`
  the agent view uses — no second clipboard path is introduced. The hint is
  refreshed each tick for the single focused pane's task, mirroring the agent
  view's per-tick `refreshClipboardCache` (so no extra RPC chattiness).

## Capabilities

### Modified Capabilities

- `hera-view`: Add the focused-pane `ctrl+y` agent-staged-clipboard copy
  binding (conditional PTY fall-through) and the per-pane "copy" border-title
  hint.

## Impact

- **Modified code:**
  - `internal/tui/hera/page.go` — `OnCopyClipboard` callback + `clipReady` hint
    state + `SetClipboardHint`; trap `ctrl+y` in `InputHandler` (gated on a
    staged payload + a focused terminal pane, else fall through to the PTY);
    render the `(ctrl+y copy)` affordance on the focused pane's border title in
    `Draw`.
  - `internal/tui/hera/panes.go` — `FocusedTerminalTaskID` (focused
    coordinator/worker pane → its bound task ID, "" for rail/details).
  - `internal/tui/clipboard.go` — `copyStagedClipboardForHeraPane(taskID)`: the
    Hera analogue of `copyStagedClipboard`, looking the payload up directly by
    task (no single-active-task cache).
  - `internal/tui/app.go` — wire `OnCopyClipboard`; refresh the Hera clipboard
    hint each tick while the Hera tab is active (`refreshHeraClipboardHint`).
  - `internal/tui/modal/help.go` + `help_test.go` — add `{"ctrl+y", ...}` to the
    "Hera View (rail)" help section (CLAUDE.md keybinding rule).
  - `README.md` — Reference appendix Hera keybinding table.
  - `context/knowledge/gotchas/hera-view.md` — the focused-pane scoping +
    conditional-intercept gotcha.
- **No new dependencies, no schema change, no new daemon RPC.** Reuses the
  existing clipboard accessor, `copyToClipboard`, and in-process-runner seam.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
