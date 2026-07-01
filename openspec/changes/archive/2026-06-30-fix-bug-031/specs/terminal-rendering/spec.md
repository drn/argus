# Terminal Rendering

## ADDED Requirements

### Requirement: Keyboard scroll suppressed for full-screen agents

The terminal pane SHALL NOT enter its own scroll mode in response to a keyboard
scroll-up or any scroll-mode-entry call while the agent's emulator is on the
alternate screen (a full-screen TUI that redraws in place and keeps no linear
terminal scrollback). Raising the scroll offset for such a pane would replay
the agent's stacked in-place frames through a fresh emulator as sequential
scrollback lines, producing interleaved garbage. The scroll-up entry points
(`ScrollUp` / accelerated scroll-up) SHALL no-op while the emulator is on the
alternate screen, and the keyboard scroll-up keys SHALL be suppressed with a
brief, transient affordance directing the user to scroll within the agent. This
complements the mouse-wheel forwarding (which hands the wheel to the agent): the
keyboard path suppresses rather than forwards, and the wheel path is unchanged.

The guard SHALL key off the live alternate-screen state and SHALL NOT latch: once
the agent leaves the alternate screen (`ESC[?1049l` on quit/exit), normal
keyboard scrollback SHALL resume. When the agent is on the main screen, or the
session is finished/replay (emulator not on the alternate screen), keyboard
scroll SHALL browse the pane's own scrollback exactly as before.

#### Scenario: Keyboard scroll-up over a full-screen (alternate-screen) agent

- **GIVEN** a pane whose emulator is on the alternate screen
- **WHEN** the user presses a scroll-up key (Shift+↑ / Shift+PgUp in the agent
  view, PgUp in a Hera pane) or a scroll-up entry call is made
- **THEN** the pane's scroll offset SHALL remain at the live tail (no scroll-mode
  entry, no frame replay)
- **AND** a brief affordance SHALL be surfaced telling the user to scroll within
  the agent

#### Scenario: Keyboard scroll over a main-screen agent

- **GIVEN** a pane whose emulator is on the main screen (or a finished/replay
  session)
- **WHEN** the user presses a scroll-up key
- **THEN** the pane SHALL scroll its own terminal scrollback as before

#### Scenario: Scrollback resumes after leaving the alternate screen

- **GIVEN** a pane whose emulator was on the alternate screen and had keyboard
  scroll suppressed
- **WHEN** the agent leaves the alternate screen (`ESC[?1049l`)
- **THEN** a subsequent keyboard scroll-up SHALL enter scroll mode and browse the
  pane's scrollback normally
