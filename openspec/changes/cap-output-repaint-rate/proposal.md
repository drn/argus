## Why

PR 1 (`repaint-panes-on-output`) made keystroke echo fast by repainting when output arrives, coalesced at an 8ms cap. A busy agent now repaints at about 38–47 frames per second, which costs roughly 10 points of one core while agents stream output. Nobody can read output that fast. Only the echo of a keystroke needs the fast cap.

## What changes

- **Two repaint rates.** Output-only frames coalesce at 40ms (25fps). A frame within 150ms after a keystroke keeps the 8ms cap, so echo stays immediate.
- **Key signal.** Every key event that reaches the global key handler stamps the pump with an atomic timestamp and shortens a pending wait, so a key pressed mid-throttle does not delay its echo by up to 40ms.
- **Nothing is dropped.** The single-slot wake channel still retains output that arrives during a throttle, so the last output of a burst always paints, at most one output interval late.
- **First byte after quiet is still immediate.**
- **Counters.** The 30-second `[repaint]` line splits frames into `echo_frames` and `output_frames`. Still off unless `ARGUS_TYPING_LOG=1`.
- **Typing log unchanged.** It still measures key press to flushed echo, so it shows whether echo stayed fast.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: output-only repaints coalesce at a slower cap than echo repaints.

## Impact

- **Code:** `internal/tui/repaint.go`, one line in `handleGlobalKey` in `internal/tui/app.go`.
- **Unchanged:** supervisor `ProtocolVersion`, `TestSupervisorSurfaceDigest`, `internal/agent/session.go`, wire formats, REST surface, no `screen.Sync()`.
- **Deployment:** a TUI relaunch only.
- **Frontend parity:** TUI-internal. The web and macOS clients paint on their own schedules and are unaffected.
- **Known edge:** bracketed paste bypasses `handleGlobalKey`, so a paste's echo paints at the output cap (at most 40ms) rather than 8ms.
