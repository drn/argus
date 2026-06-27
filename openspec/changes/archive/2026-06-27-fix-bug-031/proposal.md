## Why

**BUG-031 — scrolling up in a full-screen (alternate-screen) agent pane shows
garbled frame replay, not clean scrollback.**

When an agent runs in the "fullscreen" TUI mode it uses the alternate screen
(DECSET 1049): cursor-addressed in-place redraws with NO linear terminal
scrollback. When the user scrolls up in that pane, argus's keyboard scroll path
(`Shift+↑` / `Shift+PgUp` in the main agent view, `PgUp` in a Hera pane) and
`ScrollUp` / `AccelScrollUp` unconditionally raise `scrollOffset`, entering
argus's own `[SCROLL]` mode. That mode replays the FULL raw session-log bytes
(cursor-home, erase, cursor-position + char draws) through a fresh emulator and
reads them as sequential scrollback lines — so the stacked in-place frames pile
into interleaved/columnar garbage.

Alt-screen was already detected and handled for the MOUSE WHEEL (BUG-026, the
"Mouse wheel forwarded to full-screen agents" requirement), which forwards the
wheel to the agent as an SGR frame. But the KEYBOARD scroll path and the
scroll-mode-entry methods were left unguarded, so they still entered the garbled
replay.

## What Changes

- **A pane whose emulator is on the alternate screen SHALL NOT enter argus's own
  scroll mode.** `ScrollUp` / `AccelScrollUp` (and any other scroll-mode entry)
  no-op for an alt-screen pane instead of raising `scrollOffset` and replaying
  in-place frames as linear scrollback garbage.
- **The keyboard scroll paths suppress scroll-up for an alt-screen pane and show
  a brief affordance** ("Fullscreen agent — scroll within the agent") so the
  no-op is explained rather than silent. This covers the main agent view
  (`Shift+↑` / `Shift+PgUp`) and the Hera worker/coordinator panes (`PgUp`).
- **The guard never latches.** Once the agent leaves the alternate screen
  (`ESC[?1049l` on quit/exit), normal keyboard scrollback resumes immediately.
- **Non-alt-screen panes are unchanged** — normal and finished/replay sessions
  scroll exactly as before.
- The mouse-wheel forwarding (BUG-026) is untouched; this change is purely the
  keyboard / scroll-mode-entry guard, and it reuses the same alt-screen signal.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: keyboard scroll and scroll-mode entry are now suppressed
  (with an affordance) for alternate-screen panes, complementing the existing
  mouse-wheel forwarding — previously only the wheel was guarded, so the keyboard
  path replayed alt-screen frames as garbled scrollback.
