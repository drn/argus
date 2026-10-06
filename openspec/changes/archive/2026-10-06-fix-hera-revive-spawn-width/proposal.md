# Fix hera revive spawn width

## Why

Reviving (or size-drift-kicking) a worker from the Hera tab respawned its session at the host terminal's full size (e.g. 212 cols) while the Hera pane showing it is much narrower (e.g. 90). Claude Code positions each word with an absolute column move (`ESC[nG`); the resumed history streams into the pane's live emulator (the stream survives the restart, no reset), where columns beyond the pane width clamp to the right margin. Result: lines truncated at the left and single characters stacked down the right edge. Fullscreen toggle (Ctrl+Z x2) rebuilds the pane from the log at the authored width, which is why it "fixed" it.

## What changes

- A session (re)spawned while the Hera tab is active and a Hera pane shows that task starts at that pane's PTY size instead of the host terminal size.
- Every other spawn path keeps the host-terminal size.
