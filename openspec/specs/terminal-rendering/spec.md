# Terminal Rendering

## Purpose

Argus renders raw PTY output from an agent session directly onto the terminal screen without an ANSI-string intermediary. Inbound bytes are parsed by a VT emulator into a cell grid, and each cell is painted to the screen with its glyph and styling. This capability covers live follow-tail rendering, scrollback browsing, finished-session replay, diff display, the byte-stream filters that protect the emulator from upstream parser bugs, and the helpers that strip terminal noise to plain text. Updates rely on per-cell diffing (synchronized output) for atomic, flash-free repaints — there is no screen-wide clear-and-redraw in the steady state.
## Requirements
### Requirement: Live PTY rendering

The terminal pane SHALL feed inbound PTY bytes through a VT emulator and paint the resulting cell grid directly to the screen, mapping emulator cell glyphs and styles to screen cells without an intermediate ANSI string. In live follow-tail mode it SHALL feed only the bytes that have arrived since the last paint, rather than re-feeding the full stream on every frame.

#### Scenario: New PTY bytes arrive

- **WHEN** a live session has written more bytes than were last fed to the emulator
- **THEN** only the new (delta) bytes SHALL be fed to the emulator and the updated cell grid SHALL be painted to the screen

#### Scenario: Redraw with no new bytes

- **WHEN** a redraw is triggered (keystroke or timer tick) but no new PTY bytes have arrived and the viewport is unchanged
- **THEN** the emulator SHALL NOT be re-fed and the previous frame's painted cells SHALL be replayed unchanged

#### Scenario: Cell styling is preserved

- **WHEN** an emulator cell carries foreground/background color, bold, faint, italic, blink, reverse, strikethrough, or underline (single/double/curly/dotted/dashed) styling
- **THEN** the painted screen cell SHALL carry the equivalent style

### Requirement: Cursor visibility

The terminal pane SHALL render the cursor only when the emulator reports the cursor as visible, and SHALL default the cursor to hidden when an emulator is created or rebuilt until an explicit show-cursor sequence is processed.

#### Scenario: Hidden cursor not painted

- **WHEN** the emulator reports the cursor as hidden
- **THEN** no cursor cell SHALL be painted and a hidden cursor below the last content row SHALL NOT extend the rendered content region

#### Scenario: Visible cursor painted with high contrast

- **WHEN** the emulator reports the cursor as visible and it lies within the main screen area
- **THEN** the cell at the cursor position SHALL be painted with a fixed high-contrast color independent of the active theme

#### Scenario: Freshly built emulator defaults to hidden cursor

- **WHEN** an emulator is created or rebuilt and no show-cursor sequence has yet been processed
- **THEN** the cursor SHALL be treated as hidden

### Requirement: Scrollback browsing

The terminal pane SHALL support scrolling up into session history and back down to the live tail. While scrolled, it SHALL keep the viewed content pinned as new output arrives (anchor-lock), and SHALL clamp the scroll offset to the available scrollback. Returning to the bottom SHALL resume live follow-tail.

#### Scenario: Scroll up enters history

- **WHEN** the user scrolls up from the live tail
- **THEN** the pane SHALL display earlier content from the scrollback buffer and show a scroll indicator

#### Scenario: Anchor-lock on new output

- **WHEN** the user is scrolled up and new output arrives that grows the total line count
- **THEN** the scroll offset SHALL be increased by the growth so the viewed content stays pinned in place

#### Scenario: Scroll back to bottom resumes live

- **WHEN** the user scrolls down such that the offset reaches zero
- **THEN** the pane SHALL resume live follow-tail rendering

#### Scenario: Scroll offset clamped to available history

- **WHEN** the requested scroll offset exceeds the available scrollback
- **THEN** the offset SHALL be clamped to the maximum available scroll

### Requirement: Keyboard scroll acceleration

The terminal pane SHALL accelerate keyboard scroll steps when scroll events repeat in rapid succession, and SHALL reset to a single-line step when scrolls are spaced apart.

#### Scenario: Rapid repeats accelerate

- **WHEN** consecutive scroll events arrive within the acceleration window
- **THEN** the per-event step SHALL increase up to a capped maximum

#### Scenario: Spaced scrolls reset acceleration

- **WHEN** a scroll event arrives after the acceleration window has elapsed
- **THEN** the step SHALL reset to a single line

### Requirement: Finished-session replay

When no live session is attached but a finished session has logged output, the terminal pane SHALL reconstruct the session's content from its log for display and scrollback. The replay path SHALL run heavy log reads and emulation on a background goroutine so the rendering thread is not blocked, painting a placeholder or a best-available fallback frame while the reconstruction is in flight.

#### Scenario: Replay reconstructed from log

- **WHEN** the pane is attached to a task with logged output and no live session
- **THEN** the content SHALL be reconstructed from the session log and rendered

#### Scenario: Reconstruction in flight

- **WHEN** a replay reconstruction has been kicked off and is not yet complete
- **THEN** the pane SHALL paint the best available emulator (a stale replay or the live emulator) or a waiting placeholder rather than blocking

#### Scenario: No content available

- **WHEN** there is no live session and no logged or buffered output for the task
- **THEN** the pane SHALL display an informational message (e.g. that the session is not running or no session is active)

### Requirement: Empty and pending states

The terminal pane SHALL display contextual placeholder messaging when there is nothing to render, distinguishing a task whose worktree is still being prepared from a selected task with no running session.

#### Scenario: Pending task

- **WHEN** a task is pending (worktree being created) and no session has started
- **THEN** the pane SHALL display a launch banner instead of the no-session message

#### Scenario: Selected task not running

- **WHEN** a task is selected, has no live session, and has no content
- **THEN** the pane SHALL display a message indicating the session is not running and can be started

### Requirement: PTY size tracking

The terminal pane SHALL keep the PTY size aligned with the visible content area. When the content area dimensions change for a live session, it SHALL request a matching PTY resize. For dead or replay sessions it SHALL render at the current content-area dimensions so content reflows with the window.

#### Scenario: Panel resized for a live session

- **WHEN** the rendered content area dimensions differ from the tracked PTY size and the session is alive
- **THEN** a PTY resize to the new dimensions SHALL be requested

#### Scenario: Forced resync

- **WHEN** a one-shot forced resync is armed and a live session is drawn
- **THEN** a PTY resize SHALL be reposted even if the tracked dimensions appear unchanged, and the flag SHALL be cleared only after a live session consumes it

#### Scenario: Dead session reflows to window

- **WHEN** the session is dead or absent and the content area dimensions change
- **THEN** the replayed content SHALL be rendered at the current content-area dimensions rather than a stale PTY size

### Requirement: Paste forwarding

When focused on a live session, the terminal pane SHALL forward pasted text to the PTY as a single write wrapped in bracketed-paste sequences, rather than sending it character-by-character.

#### Scenario: Paste into a live session

- **WHEN** text is pasted while a live, alive session is attached
- **THEN** the text SHALL be written to the PTY once, wrapped in bracketed-paste start/end sequences

#### Scenario: Paste with no live session

- **WHEN** text is pasted while no live or alive session is attached
- **THEN** no PTY write SHALL occur

### Requirement: Diff display mode

The terminal pane SHALL support a diff display mode that replaces terminal rendering with a parsed unified or side-by-side diff for a named file, with independent scrolling, and SHALL restore terminal rendering on exit.

#### Scenario: Enter diff mode

- **WHEN** diff mode is entered with a diff and file name
- **THEN** the pane SHALL render the parsed diff with a header instead of terminal output

#### Scenario: Toggle split view

- **WHEN** the split toggle is invoked in diff mode
- **THEN** the view SHALL switch between unified and side-by-side rendering and reset diff scroll

#### Scenario: Exit diff mode

- **WHEN** diff mode is exited
- **THEN** the pane SHALL return to terminal rendering

### Requirement: OSC sequence filtering

Before bytes reach the live emulator, the terminal pane SHALL strip OSC sequences (`ESC ] … terminator`) from the PTY stream, treating only BEL or 7-bit ST as terminators and explicitly NOT treating a `0x9C` byte as a terminator (working around an upstream parser bug that leaks UTF-8 window titles onto the screen). The filter SHALL be stateful so a sequence split across incremental feeds is fully removed, SHALL preserve non-OSC bytes, and SHALL stop dropping after a bounded maximum so a never-terminated OSC cannot consume the stream unboundedly.

#### Scenario: OSC sequence removed

- **WHEN** an OSC sequence appears in the byte stream
- **THEN** the OSC bytes SHALL be removed and surrounding non-OSC bytes SHALL pass through unchanged

#### Scenario: OSC split across feeds

- **WHEN** an OSC sequence begins in one incremental feed and ends in a later one
- **THEN** the entire sequence SHALL still be stripped across the feed boundary

#### Scenario: 0x9C is not a terminator

- **WHEN** a `0x9C` byte (e.g. a UTF-8 continuation byte) appears inside an OSC payload
- **THEN** it SHALL NOT end the OSC and the payload SHALL continue to be dropped until a real terminator

#### Scenario: Runaway OSC is bounded

- **WHEN** an OSC payload exceeds the maximum drop budget without a recognized terminator
- **THEN** the filter SHALL stop dropping and resume passing bytes through so it can never hang

### Requirement: Escape-boundary alignment for tail feeds

When feeding a byte slice that may begin in the middle of an escape sequence (a log tail or ring-buffer tail) into a fresh emulator, the terminal pane SHALL skip to the first escape byte so the parser sees complete sequences and does not render orphan parameter bytes as text.

#### Scenario: Tail starts mid-sequence

- **WHEN** a tail slice begins partway through a CSI sequence (no leading ESC) and contains an ESC later
- **THEN** the slice SHALL be advanced to the first ESC before being fed to the emulator

#### Scenario: Already aligned or no escape

- **WHEN** the slice already starts with ESC, or contains no ESC at all
- **THEN** the slice SHALL be fed unchanged

### Requirement: Emulator panic and query-response safety

The terminal pane SHALL protect against emulator hangs and crashes: every emulator SHALL have its query-response pipe continuously drained so query sequences (DA1/DA2/DSR, etc.) do not block writes, and emulator writes SHALL recover from upstream panics rather than crashing the renderer.

#### Scenario: Query sequence does not hang

- **WHEN** input containing a terminal query sequence is written to the emulator
- **THEN** the write SHALL complete without blocking because the response pipe is drained

#### Scenario: Emulator write panics

- **WHEN** an emulator write triggers an upstream panic
- **THEN** the panic SHALL be recovered, logged, and surfaced as an error rather than crashing the rendering thread

### Requirement: Focus-regain screen repair

The screen wrapper SHALL invoke a screen-repair callback when the terminal pane regains focus (e.g. after a multiplexer window switch), to recover from any backing-store drift, while passing the focus event through unchanged. Screen-wide synchronization SHALL NOT be used for ordinary content updates.

#### Scenario: Focus regained

- **WHEN** a focus-gained event is observed
- **THEN** the configured screen-repair callback SHALL be invoked and the event SHALL still be returned for normal handling

#### Scenario: Focus lost or other event

- **WHEN** a focus-lost event or any non-focus event is observed
- **THEN** the screen-repair callback SHALL NOT be invoked

### Requirement: PTY output sanitization to plain text

The capability SHALL provide a sanitizer that converts raw PTY output into clean, human-readable plain text by stripping ANSI escape sequences, normalizing line endings and non-breaking spaces, collapsing consecutive blank lines, and removing terminal rendering noise (spinners, status bars, partial repaint frames, transient hints).

#### Scenario: ANSI sequences stripped

- **WHEN** text containing ANSI escape sequences (CSI, OSC, charset, keypad, DEC line attributes) is sanitized
- **THEN** the escape sequences SHALL be removed leaving the underlying text

#### Scenario: Noise lines removed

- **WHEN** the output contains terminal rendering noise lines (spinner frames, status bars, separators, transient timing/keybinding hints)
- **THEN** those lines SHALL be omitted from the cleaned output

#### Scenario: Whitespace normalized

- **WHEN** the output contains CRLF/CR line endings, non-breaking spaces, or runs of blank lines
- **THEN** line endings SHALL be normalized to LF, non-breaking spaces SHALL become regular spaces, consecutive blank lines SHALL collapse, and trailing blank lines SHALL be trimmed

### Requirement: Plugin-view terminal surface

The plugin-view terminal surface SHALL feed an ANSI byte stream from a channel through a VT emulator and paint the main-screen cell grid to a bordered panel, auto-sizing the emulator to the panel's inner rect each frame and forwarding focused keystrokes and pastes to a configured input channel without blocking.

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

### Requirement: Snap to bottom on user input

The terminal pane SHALL snap back to the live tail (scroll offset zero) when the user sends real input to a live session while scrolled up into history, so the typed input and the agent cursor are immediately visible. Real input means a printable key, Enter, a control character meant for the agent, or a paste. Scrollback navigation keys SHALL NOT snap, and new agent output SHALL NOT snap (anchor-lock keeps scrolled-up content pinned as output arrives).

#### Scenario: Typing while scrolled up snaps to the live tail

- **WHEN** the pane is scrolled up (offset greater than zero) and a keystroke or paste is forwarded to a live session
- **THEN** the scroll offset SHALL be reset to zero so the next frame shows the live tail with the cursor

#### Scenario: Scrollback keys do not snap

- **WHEN** the pane is scrolled up and a scrollback-navigation key (PgUp / PgDn / Shift+arrows / Home / End) is pressed
- **THEN** the scroll offset SHALL NOT be reset by the keypress and the user SHALL continue browsing history

#### Scenario: Output does not snap

- **WHEN** the pane is scrolled up and new agent output arrives
- **THEN** the scroll offset SHALL remain pinned by anchor-lock and SHALL NOT snap to the bottom

