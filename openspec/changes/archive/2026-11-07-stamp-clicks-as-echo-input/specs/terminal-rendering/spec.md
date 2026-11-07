## MODIFIED Requirements

### Requirement: Visible terminal panes repaint when output arrives

The TUI SHALL repaint when output arrives for a session shown in a terminal pane that was drawn in the most recent frame, covering the Tasks agent view pane and both Hera panes. It SHALL NOT rely on a timer to paint that output. Repaints SHALL be coalesced, and at most one such repaint SHALL be queued at a time. A repaint within 150ms after a key event reached the global key handler a pane forwarded a paste or a mouse click press or release to a live session (an echo frame) SHALL use a minimum frame interval of about 8ms; every other repaint (an output-only frame) SHALL use a minimum frame interval of about 40ms. A key event arriving while the pump is waiting out an output-only interval SHALL shorten that wait to the echo interval. The first output after a quiet period longer than the applicable interval SHALL queue a repaint immediately. Output that arrives during a wait or during a repaint SHALL NOT be lost: the last output of a burst SHALL paint no later than one output-only interval after it arrived. The output notification and the key signal SHALL NOT block the session's stream reader or the UI goroutine. This mechanism SHALL NOT call `screen.Sync()`.

#### Scenario: Echo paints without waiting for a timer

- **WHEN** a pane on screen is quiet and its session produces output
- **THEN** a repaint is queued immediately, without waiting for the spinner loop or the tick

#### Scenario: A Hera pane repaints on output

- **WHEN** a session bound to a visible Hera coordinator or agent pane produces output
- **THEN** that pane repaints the same way as the Tasks agent view

#### Scenario: Output-only frames coalesce at the slower rate

- **WHEN** a session streams output continuously and no key event has occurred in the last 150ms
- **THEN** no more than one repaint starts in any 40ms interval

#### Scenario: Keystroke echo keeps the fast cap

- **WHEN** a key event occurs while a session streams output and the echo arrives
- **THEN** the echo repaints within the 8ms interval of the previous repaint, not the 40ms one

#### Scenario: A paste echo keeps the fast cap

- **WHEN** a paste is forwarded to a live session in a visible pane and its echo arrives
- **THEN** the echo repaints at the 8ms interval, the same as a keystroke echo

#### Scenario: A forwarded click echo keeps the fast cap

- **WHEN** a mouse click press or release is forwarded to a live session in a visible pane and the agent redraws in response
- **THEN** the redraw repaints at the 8ms interval, the same as a keystroke echo

#### Scenario: Wheel scrolling and unforwarded clicks do not open the echo window

- **WHEN** the pane forwards a mouse wheel event, or a click is not forwarded because the pane is unfocused or has no live session
- **THEN** the echo window is not opened and output repaints at the output-only interval

#### Scenario: A key shortens a throttle in progress

- **WHEN** the pump is waiting out an output-only interval and a key event arrives
- **THEN** the remaining wait is recomputed using the 8ms interval

#### Scenario: The end of a burst always paints

- **WHEN** a burst of output ends while the pump is throttling
- **THEN** one further repaint paints the final output no later than one output-only interval after it arrived

#### Scenario: Output for a session not on screen causes no repaint

- **WHEN** a session produces output and no pane drawn in the most recent frame shows it
- **THEN** that output does not queue a repaint

#### Scenario: Only the drawn pane counts as visible

- **WHEN** a Hera pane is fullscreen and the other pane is not drawn
- **THEN** output for the session in the undrawn pane does not queue a repaint

#### Scenario: A quiet pane causes no repaints

- **WHEN** a pane on screen shows a session that produces no output
- **THEN** this mechanism queues no repaints

#### Scenario: A pane bound to a new session is picked up

- **WHEN** a visible pane is rebound to a different session
- **THEN** after the next frame, output from the new session repaints and output from the old session does not

#### Scenario: A session ending repaints its pane

- **WHEN** a session shown in a visible pane ends
- **THEN** one repaint is queued so the finished state appears without waiting for the tick

#### Scenario: Frames are counted by kind

- **WHEN** the typing log is enabled and the pump draws
- **THEN** the periodic `[repaint]` line reports separate counts of echo frames and output-only frames
