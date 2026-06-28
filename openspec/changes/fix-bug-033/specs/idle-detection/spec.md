# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: Detection matches the emulated screen for cursor-addressed (alt-screen) prompts

The system SHALL recognize a selection-prompt / needs-input signal even when the
agent paints its prompt with cursor-addressed in-place redraws (a fullscreen /
alt-screen agent), where the prompt glyphs are not linearly adjacent in the raw
output stream. Detection SHALL therefore be able to match against the VISIBLE
SCREEN reconstructed by feeding the recent output tail through a terminal
emulator sized to the session's current dimensions, in addition to the raw
ANSI-stripped stream. Stripping ANSI escapes alone does NOT apply cursor
positioning, so a cursor-addressed prompt is invisible to a raw-text match; the
emulated screen places the glyphs where they actually render.

The raw-text match SHALL remain the fast path: a linear (main-screen) agent
whose prompt IS linearly present in the stream MUST be detected exactly as before
and MUST NOT depend on emulation. Emulation SHALL be used as a fallback when the
raw match misses (or unconditionally, provided linear behavior is preserved).
When the session's true dimensions are unknown, a sane default terminal size
(80×24) SHALL be used. This guarantee applies to BOTH the selection-prompt signal
and the never-idle content-stability pass, so an alt-screen prompt is flagged
without waiting for a view-triggered resize/repaint.

#### Scenario: Cursor-addressed alt-screen prompt is detected via the emulated screen

- **WHEN** a running session's recent output paints a numbered-selection prompt
  using cursor-positioning such that the selection cursor and the first option are
  not linearly adjacent in the raw byte stream (so a raw ANSI-stripped match
  misses)
- **THEN** the system reconstructs the visible screen, finds the selection
  signature there, and reports the agent is waiting for input

#### Scenario: Linear prompt is still detected without emulation

- **WHEN** a session's selection prompt is linearly present in the raw output
  stream
- **THEN** the system detects it via the raw-text fast path, exactly as before

#### Scenario: Plain alt-screen output without a prompt is not flagged

- **WHEN** a fullscreen agent is producing ordinary work output with no selection
  prompt on the visible screen
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Never-idle alt-screen prompt is flagged once the emulated screen is stable

- **WHEN** a running, never-idle session shows a cursor-addressed selection prompt
  and its EMULATED screen is unchanged across consecutive detection ticks (only
  off-screen repaint / spinner chrome differs)
- **THEN** the system reports the agent is waiting for input

#### Scenario: Streaming alt-screen agent is not flagged

- **WHEN** a fullscreen agent's emulated screen content changes between detection
  ticks
- **THEN** the system reports the agent is not waiting for input
