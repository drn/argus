## ADDED Requirements

### Requirement: Mouse wheel forwarded to full-screen agents

When the live agent has switched to the alternate screen (a full-screen TUI that has grabbed the screen and wants pointer input itself), the terminal pane SHALL forward mouse wheel events to the agent as SGR mouse frames (`ESC [ < 64 ; Cx ; Cy M` for wheel-up, `65` for wheel-down) over the session input, with coordinates 1-based relative to the pane's inner rect and clamped to that rect, rather than scrolling the pane's own terminal scrollback. When the agent is on the main screen, or there is no live session (finished/replay), the pane SHALL scroll its own terminal scrollback as before. Diff mode is unaffected (the wheel scrolls the diff).

This mirrors how a real terminal hands the wheel to the foreground full-screen application, which renders in place and therefore leaves no terminal scrollback to scroll.

#### Scenario: Wheel over a full-screen (alternate-screen) agent

- **GIVEN** a live agent session whose emulator is on the alternate screen
- **WHEN** the user scrolls the mouse wheel up over the pane
- **THEN** an SGR wheel-up frame SHALL be written to the agent session input
- **AND** the pane's own scroll offset SHALL remain at the live tail (not advanced into local scrollback)

#### Scenario: Wheel over a main-screen agent

- **GIVEN** a live agent session whose emulator is on the main screen
- **WHEN** the user scrolls the mouse wheel up over the pane
- **THEN** the pane SHALL scroll its own terminal scrollback
- **AND** nothing SHALL be written to the agent session input

#### Scenario: Wheel over a finished or replayed session

- **GIVEN** the pane shows a session with no live handle (finished / on-disk log replay)
- **WHEN** the user scrolls the mouse wheel
- **THEN** the pane SHALL scroll its own terminal scrollback (no forwarding), so the session's recorded history remains browsable
