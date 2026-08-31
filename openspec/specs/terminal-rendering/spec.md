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

The available scrollback SHALL be reconstructed from the on-disk session log, which retains the full session history across TUI restarts (the log is truncated only when a session starts, not when a viewer reattaches to a still-running session). Reconstruction SHALL load a bounded window of the log rather than eagerly replaying the entire (possibly multi-MB) log. When the user scrolls PAST the currently-loaded window and older bytes remain in the log, the pane SHALL read further back from the on-disk log to extend the loaded scrollback, reaching strictly older history on each such extension until the whole log is loaded or a bounded maximum single read is reached. The extension SHALL NOT depend on a bytes-per-visible-line estimate (which underestimates how far back a given number of lines reaches for escape-dense output), so scrollback is not stuck at the initially-loaded window after a reattach.

The scrollback replay SHALL be emulated at the width the scrollback bytes were AUTHORED for — resolved from the session's live PTY size, then the persisted `.size` sidecar, with the pane inner width as the floor (never narrower than the pane) — and then clipped to the pane. Re-emulating scrollback that was authored at a wider PTY size in a narrower pane clamps absolute cursor positioning at the right edge and turns the agent's in-place redraws into fragmented, overlapping garbage; emulating at the authored width and clipping avoids this. In the common case where the pane matches the session PTY size, the authored width equals the pane width and nothing changes.

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

#### Scenario: Scroll past the loaded window extends from the on-disk log

- **WHEN** the user scrolls up past the currently-loaded scrollback window of a live session whose on-disk log holds older history than is currently loaded
- **THEN** the pane SHALL read further back from the on-disk log so the loaded scrollback reaches strictly older content (advancing the first loaded byte earlier), rather than remaining pinned at the initially-loaded window — until the whole log is loaded or the bounded maximum single read is reached

#### Scenario: Extension does not eagerly replay the whole log

- **WHEN** a scroll-past-the-window extension occurs on a very large on-disk log
- **THEN** the read SHALL remain bounded by the maximum single-read size rather than loading the entire log at once

#### Scenario: Wide scrollback renders without width-mismatch corruption

- **WHEN** the user scrolls up in a live session whose scrollback was authored at a PTY size wider than the current pane
- **THEN** the replay SHALL be emulated at the authored (wider) size and clipped to the pane, so content positioned beyond the pane width is clipped rather than clamped to the pane's right edge — no fragmented one-word-per-line reflow, no stranded single-character right-edge column, and no re-shown live footer / PTY input prompt in the scrollback

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

### Requirement: Live emulator answers terminal capability queries

The pane's live PTY emulator SHALL report an assumed terminal
background/foreground color and SHALL forward the emulator's auto-generated
responses to terminal capability queries (including `OSC 10`/`OSC 11`
foreground/background color queries) into the agent process's stdin, so an
agent CLI that conditions its own rendering on a color query receives a real
answer instead of silence. The emulator's internal response pipe SHALL
continue to be drained on its own goroutine so a query sequence never blocks
the emulator's `Write` (the pre-existing hang-avoidance behavior is
preserved); forwarding a drained response into the agent process's stdin
SHALL happen on a separate goroutine from that drain loop, since the stdin
write (unlike the prior discard-only behavior) can itself block if the agent
process is not promptly reading its input — the drain loop SHALL NOT be
blocked by a slow or stalled forward. A response that cannot be handed off
without blocking (the forwarding path is still busy with an earlier
response) SHALL be dropped rather than blocking the drain loop.

The forwarded response SHALL be delivered as SYSTEM-classified input (the
same delivery classification reliable-notify pane delivery uses), never as
user-classified input. A capability-query response is generated
automatically by the emulator and never represents the human answering a
prompt; delivering it as user input would let the idle-detection
capability's clear-on-input logic (which counts only genuine user-delivered
input) mistake it for the user resolving a pending needs-input flag, clearing
`(?)` on a session nobody actually answered — and, since the query/response
cycle can recur while the pane stays focused, re-flagging and re-clearing on
every detection tick as long as focus is held.

Each live emulator's forwarded responses SHALL only be delivered to the
session that was the pane's current session when that emulator was created.
If the pane's current session has since changed (a new session attached,
which always replaces the live emulator), a response generated by the
now-superseded emulator SHALL be dropped rather than delivered to the new
session or to the old one.

Replay and preview emulators (scrollback browsing, task-list preview) SHALL
NOT forward responses into any process — they continue to drain to
`io.Discard`, since they reconstruct historical output for a process that
may not be running or isn't the same live process.

#### Scenario: Live emulator answers a background-color query

- **WHEN** the live emulator processes an `OSC 11 ?` (query background color)
  sequence from the agent process
- **THEN** the emulator's generated response is written to the agent
  process's stdin via the terminal adapter, reporting the assumed background
  color

#### Scenario: Live emulator answers a foreground-color query

- **WHEN** the live emulator processes an `OSC 10 ?` (query foreground color)
  sequence from the agent process
- **THEN** the emulator's generated response is written to the agent
  process's stdin via the terminal adapter, reporting the assumed foreground
  color

#### Scenario: Forwarded response is delivered as system input, not user input

- **WHEN** the live emulator's generated query response is forwarded to the
  agent process
- **THEN** it is delivered via the session's system-input path (advancing
  only the work-cycle timestamp) rather than the user-input path, so it is
  NEVER treated by the idle-detection capability's clear-on-input logic as
  the user answering a pending needs-input prompt

#### Scenario: No live session attached drops the response silently

- **WHEN** the live emulator generates a query response but the pane has no
  current session (detached, not yet attached, or exited)
- **THEN** the response is discarded rather than written anywhere, and no
  error occurs

#### Scenario: Replay and preview emulators remain discard-only

- **WHEN** a replay or preview emulator (not the pane's live emulator)
  processes a terminal capability query
- **THEN** its generated response is drained to `io.Discard` as before and is
  never forwarded to any process

#### Scenario: A slow or stalled forward does not block the drain loop

- **WHEN** the agent process is not promptly reading its stdin, so
  forwarding a response would block
- **THEN** the emulator's response pipe continues to be drained (the
  emulator's own `Write` is never blocked by the stalled forward), and any
  response that arrives while forwarding is still stuck is dropped rather
  than queued indefinitely

#### Scenario: A response from a superseded emulator is dropped, not misdelivered

- **WHEN** a new session is attached (replacing the pane's live emulator)
  before an older, now-superseded emulator's in-flight query response is
  forwarded
- **THEN** that response is dropped rather than delivered to either the new
  session or the old one

### Requirement: History reads anchor on ring content, not on byte-counter arithmetic

The terminal pane SHALL locate the bytes it needs in the session log by matching the ring tail it already holds against the log's own tail, SHALL NOT use its fed-byte counter (or any comparison between the session handle's total and the log's file size) as an absolute log offset, and SHALL only ever record that fed-byte counter in the session handle's own byte space.

The fed-byte counter counts bytes in the **session handle's** byte space. For a daemon-backed
session handle that space is offset from the on-disk session log's byte offsets by an
arbitrary amount, because the daemon replays only its own bounded ring on stream attach
rather than the session from byte zero.

#### Scenario: Ring-wrap catch-up on a daemon-backed session

- **WHEN** the live emulator needs a catch-up range that the ring tail no longer covers, and the session handle's byte counter is offset from the session log's byte offsets
- **THEN** the pane SHALL feed the log bytes that immediately precede and include the ring tail's content, never the bytes at the raw counter offset

#### Scenario: Ring tail cannot be located in the log

- **WHEN** the ring tail's content cannot be found in the searched region of the session log
- **THEN** the pane SHALL fall back to the existing approximate history rebuild rather than feeding an unanchored range

#### Scenario: Full replay when the log already covers the ring

- **WHEN** a full history rebuild reads a log tail that already contains the ring tail's content
- **THEN** the assembled history SHALL be trimmed to end exactly at the ring tail's end, and the recorded fed-total SHALL be the handle's own ring total

#### Scenario: Full replay when the log lags the ring

- **WHEN** a full history rebuild reads a log tail that holds only a leading part of the ring tail
- **THEN** the uncovered ring suffix SHALL be appended only after verifying that the log's own tail matches the ring tail's corresponding prefix

#### Scenario: Full replay when log and ring cannot be reconciled

- **WHEN** a full history rebuild cannot locate any overlap between the log tail and the ring tail
- **THEN** the pane SHALL feed the ring alone rather than splicing two non-contiguous byte ranges together

