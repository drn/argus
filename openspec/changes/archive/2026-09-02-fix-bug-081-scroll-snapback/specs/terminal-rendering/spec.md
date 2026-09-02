# Terminal Rendering

## MODIFIED Requirements

### Requirement: Mouse wheel forwarded to full-screen agents

When the live agent has switched to the alternate screen (a full-screen TUI that has grabbed the screen and wants pointer input itself), the terminal pane SHALL forward mouse wheel events to the agent as SGR mouse frames (`ESC [ < 64 ; Cx ; Cy M` for wheel-up, `65` for wheel-down) over the session input, with coordinates 1-based relative to the pane's inner rect and clamped to that rect, rather than scrolling the pane's own terminal scrollback. When the agent is on the main screen, the pane SHALL scroll its own terminal scrollback as before. Diff mode is unaffected (the wheel scrolls the diff).

This mirrors how a real terminal hands the wheel to the foreground full-screen application, which renders in place and therefore leaves no terminal scrollback to scroll.

Forwarding requires a LIVE session to write to. A pane with no live session SHALL NOT forward the wheel anywhere. When such a pane's RECONSTRUCTED content is itself on the alternate screen, the pane SHALL nevertheless NOT scroll its own scrollback either: an in-place full-screen recording contains no linear scrollback to browse, so raising the scroll offset only produces a scroll-mode entry that the next paint's clamp immediately undoes. Scrolling a finished or replayed session SHALL remain fully available whenever its recording is line-oriented (main screen).

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

#### Scenario: Wheel over a finished or replayed main-screen session

- **GIVEN** the pane shows a session with no live handle (finished / on-disk log replay) whose recording is line-oriented
- **WHEN** the user scrolls the mouse wheel
- **THEN** the pane SHALL scroll its own terminal scrollback (no forwarding), so the session's recorded history remains browsable

#### Scenario: Wheel over a replayed full-screen recording does not snap back

- **GIVEN** the pane has no live emulator because it renders through the replay path (no live session, or a session handle that is present but no longer alive while the agent process runs on)
- **AND** the reconstructed content is on the alternate screen (a full-screen agent's in-place repaint, carrying no linear scrollback)
- **WHEN** the user scrolls the mouse wheel up over the pane, repeatedly
- **THEN** nothing SHALL be written to any session input
- **AND** the pane's scroll offset SHALL remain at the tail on every notch — it SHALL NOT rise and then be clamped back to the tail by the following paint

### Requirement: Keyboard scroll suppressed for full-screen agents

The terminal pane SHALL NOT enter its own scroll mode in response to a keyboard
scroll-up or any scroll-mode-entry call while the content it is rendering is on
the alternate screen (a full-screen TUI that redraws in place and keeps no
linear terminal scrollback). Raising the scroll offset for such a pane would
replay the agent's stacked in-place frames through a fresh emulator as
sequential scrollback lines, producing interleaved garbage — and, because such
content's available scrollback is zero, the raised offset is clamped straight
back to the tail on the next paint, so the user sees the view twitch up and snap
back on every keypress or wheel notch. The scroll-up entry points (`ScrollUp` /
accelerated scroll-up) SHALL no-op for such content, and the keyboard scroll-up
keys SHALL be suppressed with a brief, transient affordance. This complements
the mouse-wheel forwarding (which hands the wheel to a live agent): the keyboard
path suppresses rather than forwards, and the wheel path is unchanged.

The alternate-screen determination SHALL describe the content the pane is
actually rendering, NOT solely its live emulator. The live emulator SHALL remain
the authority whenever the pane has one. It does not always have one: the live
emulator is created only while rendering the live tail of an ALIVE session, so a
pane rendering through the replay path — with no live session, or with a session
handle that is present but no longer alive while the agent process runs on —
has none for its whole lifetime. For such a pane the determination SHALL fall
back to the alternate-screen state of the reconstructed replay content. That
recorded state SHALL be captured when the replay reconstruction is built and
discarded whenever that reconstruction is, so it describes only the content
currently loaded and can never latch.

The guard SHALL NOT latch: once the agent leaves the alternate screen
(`ESC[?1049l` on quit/exit), or when the content being rendered is main-screen
(live or replayed), normal keyboard scrollback SHALL resume and browse the
pane's own scrollback exactly as before.

The transient affordance SHALL distinguish the two cases, since only one of them
leaves the user somewhere to go. When the pane HAS a live session, the affordance
SHALL direct the user to scroll within the agent (the mouse wheel is forwarded
to it, so the agent can scroll its own view). When the pane has no live session,
the affordance SHALL instead state that the full-screen output has no recorded
scrollback — directing the user at an agent that is not reachable would send
them after something that is not running.

#### Scenario: Keyboard scroll-up over a live full-screen (alternate-screen) agent

- **GIVEN** a pane with a live session whose emulator is on the alternate screen
- **WHEN** the user presses a scroll-up key (Shift+↑ / Shift+PgUp in the agent
  view, PgUp in a Hera pane) or a scroll-up entry call is made
- **THEN** the pane's scroll offset SHALL remain at the live tail (no scroll-mode
  entry, no frame replay)
- **AND** the affordance SHALL tell the user to scroll within the agent

#### Scenario: Keyboard scroll-up over a replayed full-screen recording

- **GIVEN** a pane rendering through the replay path (no live emulator) whose
  reconstructed content is on the alternate screen
- **WHEN** the user presses a scroll-up key
- **THEN** the pane's scroll offset SHALL remain at the tail
- **AND** the affordance SHALL state that the full-screen output has no recorded
  scrollback, rather than directing the user to scroll within an agent that is
  not running

#### Scenario: Keyboard scroll over a main-screen agent

- **GIVEN** a pane whose content is on the main screen — live, or a
  finished/replay session with a line-oriented recording
- **WHEN** the user presses a scroll-up key
- **THEN** the pane SHALL scroll its own terminal scrollback as before
- **AND** no affordance SHALL be surfaced

#### Scenario: Scrollback resumes after leaving the alternate screen

- **GIVEN** a pane whose emulator was on the alternate screen and had keyboard
  scroll suppressed
- **WHEN** the agent leaves the alternate screen (`ESC[?1049l`)
- **THEN** a subsequent keyboard scroll-up SHALL enter scroll mode and browse the
  pane's scrollback normally

#### Scenario: A live emulator outranks the recorded replay state

- **GIVEN** a pane whose last replay reconstruction was on the alternate screen
- **WHEN** the pane acquires a live emulator that is on the main screen
- **THEN** the pane SHALL report main-screen and scroll normally, so a stale
  recorded state can never suppress scrolling on live main-screen content

#### Scenario: Discarding the replay reconstruction discards its recorded state

- **GIVEN** a pane reporting alternate-screen from its replay reconstruction
- **WHEN** that reconstruction is discarded (terminal state reset on task
  switch/resize, or the replay cache invalidated)
- **THEN** the recorded alternate-screen state SHALL be discarded with it, so the
  pane no longer reports alternate-screen from content it is no longer holding
