## Why

Typing into an agent's terminal in Argus lags noticeably, while vanilla Claude Code does not. There are two independent causes: the screen repaints on timers instead of when output arrives, and a once-per-second scan stalls the UI goroutine that handles keystrokes.

This change fixes the first cause and adds the measurement that every later change is judged by. Echo in a Hera pane can currently wait up to a full second for a repaint, and there is no way to see how long a keystroke really takes.

This is the first of three changes. `reduce-needs-input-scan-cost` and `move-needs-input-scan-off-ui-thread` address the second cause.

## What changes

- **Timing log.** Each keystroke forwarded to an agent logs its latency through `uxlog`, from the key press to the echo being painted. The line breaks that time into its parts, including time spent waiting behind the tick stall, so each fix gets the right credit. No key content is ever logged.
- **Slow tick log.** A tick closure that takes 16ms or longer logs its duration.
- **Latency tool.** A new developer tool, `cmd/argus-latency`, reports percentiles from the timing log and measures UI goroutine stalls using the method from the original investigation.
- **Before baseline.** The timing log ships as its own commit and is measured in a relaunched TUI before the repaint change lands.
- **Output-driven repaint.** One repaint pump covers the Tasks agent view and both Hera panes. It repaints when output arrives for a pane that is on screen, coalesced under a frame cap of about 8ms.
- **Timers removed.** The 16ms follow-up check after each keystroke and the 200ms output poll are removed.
- **PTY size sync kept.** The 200ms loop stays, but only for PTY size sync.
- **Remote sessions.** The `--remote` session handle delivers output to attached writers instead of ignoring them, so the pump works there too.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: visible panes repaint when output arrives, and keystroke latency and slow ticks are logged.
- `agent-view`: the per-task loop becomes a PTY size sync loop and no longer drives repaints.
- `remote-tui`: remote sessions deliver streamed output to attached writers.

## Impact

- **Code:** `internal/tui` (`app.go`, `lazyscreen.go`, new pump and latency tracker files), `internal/tui/terminal` (pane draw record), `internal/tui/hera` (`panes.go` key path), `internal/apiclient/session.go`, new `cmd/argus-latency` and `internal/latencyreport`.
- **Unchanged:** `internal/agent/session.go`, `internal/daemon/client`, and all wire formats. See supervisor compatibility in `design.md`.
- **Deployment:** a TUI relaunch only. No daemon bounce and no supervisor restart.
- **Frontend parity:** TUI-internal. The web and macOS clients are unaffected, and the REST surface does not change.
