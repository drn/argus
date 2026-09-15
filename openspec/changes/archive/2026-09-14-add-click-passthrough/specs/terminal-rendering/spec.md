# Terminal Rendering

## ADDED Requirements

### Requirement: Mouse click passthrough to a live agent

The terminal pane SHALL treat a plain left click differently depending on
focus and session state, using `MouseLeftDown` as the decision point and
`MouseLeftUp` to deliver the matching half of the SGR press/release pair (the
synthetic `MouseLeftClick` that tview fires after `MouseLeftUp` is ignored for
click handling, since it would otherwise duplicate the decision already made
on `MouseLeftDown`):

- When the pane does NOT currently have focus, the click SHALL switch focus
  to the pane (and fire the click callback if one is configured) exactly as
  before, and SHALL NOT be forwarded to any agent.
- When the pane already has focus and a live agent session is attached, the
  click SHALL be forwarded to the agent's PTY as an SGR mouse press
  (`ESC [ < 0 ; Cx ; Cy M`) on `MouseLeftDown` and the matching release
  (`ESC [ < 0 ; Cx ; Cy m`) on `MouseLeftUp`, with coordinates 1-based
  relative to the pane's inner rect and clamped to that rect. The focus
  callback SHALL NOT fire in this case (the pane is already focused).
- When the pane already has focus and has no live agent session, the click
  SHALL fire the click callback if one is configured (unchanged fallback for
  a dead / replayed / diff-mode pane), and SHALL NOT be forwarded anywhere.

Unlike wheel forwarding, click passthrough is gated purely on pane focus state
and session liveness — NOT on whether the agent owns the alternate screen.
Claude Code implements click-driven affordances (e.g. a plain-text "Jump to
bottom (click)" line, with no OSC-8 hyperlink) that are not tied to
full-screen mode, so gating on alt-screen would miss them.

#### Scenario: Click on an unfocused pane focuses it, not forwarded

- **WHEN** the pane does not have focus and the user left-clicks inside it
- **THEN** the pane's focus callback SHALL be invoked and no SGR mouse bytes
  SHALL be written to any session

#### Scenario: Click on a focused pane with a live session forwards press and release

- **WHEN** the pane already has focus, a live agent session is attached, and
  the user performs a plain left click
- **THEN** an SGR left-button press SHALL be written on `MouseLeftDown`
  followed by the matching SGR release on `MouseLeftUp`, and the focus
  callback SHALL NOT be invoked

#### Scenario: Click on a focused pane with no live session falls back to the click callback

- **WHEN** the pane already has focus and has no live session (dead, replayed,
  or in diff mode)
- **THEN** the pane's click callback SHALL be invoked if one is configured,
  and no SGR mouse bytes SHALL be written to any session

## MODIFIED Requirements

### Requirement: Plugin-view terminal surface

The plugin-view terminal surface SHALL feed an ANSI byte stream from a channel through a VT emulator and paint the main-screen cell grid to a bordered panel, auto-sizing the emulator to the panel's inner rect each frame and forwarding focused keystrokes and pastes to a configured input channel without blocking. It SHALL also forward a plain left click as an SGR mouse press/release pair over the same input channel, mirroring its existing wheel forwarding — the surface is always the focused widget while its page is shown, so click forwarding, like wheel forwarding, is unconditional (no focus-vs-forward branch).

#### Scenario: Stream chunk rendered

- **WHEN** a non-empty chunk arrives on the source channel
- **THEN** the chunk SHALL be fed to the emulator, the touched counter SHALL increment, and a redraw SHALL be requested if a redraw callback is set

#### Scenario: Panel resized

- **WHEN** the panel's inner rect changes between draws
- **THEN** the emulator SHALL be resized to the new inner dimensions

#### Scenario: Focused input forwarded

- **WHEN** the pane is focused, has an input-back channel configured, and receives a keystroke or paste
- **THEN** the encoded bytes SHALL be sent to the input channel, dropping silently if the channel is full

#### Scenario: No input channel configured

- **WHEN** no input-back channel is configured
- **THEN** the pane SHALL be read-only and forward no input

#### Scenario: Left click forwarded as SGR press/release

- **WHEN** the user performs a plain left click over the plugin-view terminal surface
- **THEN** an SGR left-button press SHALL be sent to the input-back channel on `MouseLeftDown` and the matching SGR release SHALL be sent on `MouseLeftUp`, dropping silently if the channel is full or unset
