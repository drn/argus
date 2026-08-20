# Answer live-emulator terminal capability queries instead of discarding them

## Why

Argus's live PTY emulator (`x/vt`) deliberately discards any response it
generates to terminal capability queries — `NewDrainedEmulator` drains the
emulator's response pipe to `io.Discard` specifically to avoid a hang bug
(the emulator's internal `io.Pipe` blocks forever if nobody reads it; see
`gotchas/pty-terminal.md`). That fix was correct for the hang, but it also
means an agent CLI that queries the terminal at startup — e.g. Codex sends
`OSC 11` ("what's your background color?") on every session start — never
gets an answer, because nothing forwards the emulator's generated reply back
into the child process's stdin.

Confirmed via a real Codex v0.144.5 session log captured from a live Argus
task: Codex sends `OSC 10`/`OSC 11` queries at startup, and across every
occurrence of its idle composer/placeholder text in that session, it draws
zero background/reverse-video styling — it only colors the *submitted
prompt* history box (a separate, unconditional `\x1b[7m`). This is
consistent with Codex conservatively skipping a background-dependent
highlight it can't safely draw without knowing the terminal's background —
and matches the reported symptom exactly: the same composer state shows a
highlighted background band when Codex runs in a real terminal (which
answers OSC 11), but not when nested in Argus (which doesn't).

## What Changes

- The pane's **live** `x/vt` emulator now reports an assumed terminal
  background/foreground color (`SetBackgroundColor`/`SetForegroundColor`) and
  forwards its auto-generated query responses (OSC 10/11, and anything else
  `x/vt` answers) into the agent process's stdin via the existing
  `TerminalAdapter.WriteInput`, instead of discarding them. The drain-to-avoid-hang
  behavior is preserved — responses are still drained asynchronously, just
  routed to the real PTY instead of `io.Discard`.
- The stdin write a forward now performs (unlike the prior discard sink) can
  itself block if the agent isn't reading its input, so forwarding runs on
  its own goroutine, decoupled from the drain loop, over a small bounded
  queue — a response that can't be handed off without blocking is dropped
  rather than stalling the drain loop (which would otherwise hang the
  emulator's next `Write`).
- Each live emulator's forwarded responses are only delivered to the session
  that was current when that emulator was created; if the pane has since
  moved to a different session (a new one attached, which always replaces
  the live emulator), a stale, superseded emulator's in-flight response is
  dropped instead of being misdelivered to the wrong session.
- Replay and preview emulators (scrollback browsing, task-list preview) are
  **unchanged** — they reconstruct historical output for a process that may
  not be running (or isn't the same live process), so forwarding responses
  into them would be meaningless or cross-wired. They keep using the existing
  discard-only `NewDrainedEmulator`.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: the live PTY emulator additionally answers terminal
  capability queries (OSC 10/11 today) by forwarding a real response into the
  agent process, using an assumed background/foreground color rather than
  silence.

## Impact

- **Modified code:** `internal/tui/terminal/terminalpane.go` — new
  `NewLiveEmulator` constructor (used only by the pane's live emulator path,
  `newTrackedEmulatorWithCallback`); a `forwardEmulatorResponse` method that
  looks up the pane's current session under its existing mutex (session can
  change across attach/detach during the emulator's lifetime) and writes to
  it if non-nil.
- **No breaking changes.** Additive behavior on the live emulator only;
  replay/preview paths and the existing hang-avoidance drain are untouched.
- **Not addressed here:** querying the *real* outer terminal (the one Argus
  itself runs inside) for its actual background/foreground color and
  forwarding that true value through. Argus's own chrome never assumes a
  background (`tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault`
  everywhere), so there's no existing "real" color to relay — doing this
  properly would mean Argus querying its own controlling terminal over its
  own stdout/stdin, which risks conflicting with tcell's ownership of that
  fd's raw-mode state. Out of scope for this fix; the assumed dark-terminal
  default is a deliberate, named simplification, not an oversight.
