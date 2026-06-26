# Agent View

## Purpose

The agent view is the runtime-agnostic state and supporting widgets behind the screen where a user watches and interacts with a running agent session. It tracks which panel has focus, terminal scrollback position, and the file-diff viewer's mode and scroll, independent of the tcell/tview rendering layer. It also provides a read-mostly streaming pane that renders bytes arriving on a channel — used for plugin and settings stream sections — with optional keystroke forwarding back to the source.
## Requirements
### Requirement: Agent view default and reset state

The agent view state SHALL default to focusing the terminal panel with the diff viewer in side-by-side (split) mode and no task loaded. Resetting the state for a new task SHALL restore terminal focus, clear scroll position, last output, worktree directory, and git refresh timing, and return the diff viewer to its split default while recording the new task identity.

#### Scenario: Fresh state defaults

- **WHEN** a new agent view state is created
- **THEN** focus is on the terminal panel, the diff viewer is in split mode, and no task is loaded

#### Scenario: Reset for a new task

- **WHEN** the state is reset with a new task ID and name after a prior task left non-default values
- **THEN** the task ID and name are updated, focus returns to the terminal panel, scroll offset is zero, last output is cleared, worktree directory is empty, and the diff viewer is back in split mode

### Requirement: Focus navigation between focusable panels

The agent view SHALL allow focus to move between the terminal panel (leftmost focusable) and the files panel (rightmost focusable). Moving focus left from the terminal panel and right from the files panel SHALL be no-ops, so focus never leaves the focusable range.

#### Scenario: Move right then left

- **WHEN** focus is on the terminal panel and the user moves focus right
- **THEN** focus is on the files panel, and moving focus left returns to the terminal panel

#### Scenario: Edges are clamped

- **WHEN** the user moves focus left from the terminal panel or right from the files panel
- **THEN** focus stays unchanged

### Requirement: Terminal scrollback bounds

The agent view SHALL scroll the terminal view up toward older output and down toward the tail. Scrolling down SHALL never move the offset below zero. Scrolling up SHALL clamp the offset to the maximum scrollable distance only when the total line count is known and positive; when the line count is unknown the offset SHALL be allowed to grow freely.

#### Scenario: Scroll up clamps to content extent when known

- **WHEN** the user scrolls up by more lines than available against a known total line count larger than the display height
- **THEN** the scroll offset is clamped to the total line count minus the display height

#### Scenario: Scroll up grows freely when total is unknown

- **WHEN** the user scrolls up while the total line count is unknown
- **THEN** the scroll offset grows by the requested amount without clamping

#### Scenario: Scroll up is bounded to zero for small content

- **WHEN** the user scrolls up but the content fits entirely within the display height
- **THEN** the scroll offset stays at zero

#### Scenario: Scroll down never goes negative

- **WHEN** the user scrolls down past the tail of the output
- **THEN** the scroll offset settles at zero

### Requirement: Git status refresh gating

The agent view SHALL report that a git status refresh is needed only when a task is loaded and the configured minimum interval has elapsed since the last refresh. With no task loaded, a refresh SHALL never be reported as needed.

#### Scenario: No task means no refresh

- **WHEN** no task is loaded
- **THEN** a git refresh is not needed

#### Scenario: Stale or never-refreshed task needs refresh

- **WHEN** a task is loaded and it has never been refreshed, or the last refresh is older than the minimum interval
- **THEN** a git refresh is needed

#### Scenario: Recently refreshed task does not refresh

- **WHEN** a task is loaded and was refreshed within the minimum interval
- **THEN** a git refresh is not needed

### Requirement: Diff viewer enter, scroll, and exit

The agent view SHALL enter diff mode for a named file with the diff scroll reset to the top, and SHALL scroll the diff up (clamped at zero) and down (clamped at the total diff lines minus the visible rows). Exiting diff mode SHALL clear the active flag, scroll position, and file name, but SHALL preserve the user's split-versus-unified preference across diff opens within the session.

#### Scenario: Enter diff resets scroll

- **WHEN** the user enters diff mode for a file
- **THEN** the diff viewer becomes active with that file name and a scroll offset of zero

#### Scenario: Diff scroll is bounded

- **WHEN** the user scrolls the diff down past the last line or up past the top
- **THEN** the scroll offset clamps to the total lines minus visible rows at the bottom and to zero at the top

#### Scenario: Exit preserves split preference

- **WHEN** the user toggles the diff to unified mode and then exits and re-enters diff mode
- **THEN** the diff viewer is no longer active after exit, its file name and scroll are cleared, and the unified preference persists across the next diff open

### Requirement: Stream pane buffers and renders streamed bytes

A stream pane SHALL consume byte chunks arriving on its source channel into a bounded internal buffer that never exceeds its configured maximum, dropping the oldest bytes when the cap is exceeded. It SHALL render the trailing lines that fit inside a bordered panel, with newlines forcing line breaks and runes past the panel width wrapping to a new line. Multi-byte UTF-8 glyphs SHALL be preserved as whole runes, ANSI escape sequences SHALL be stripped, and control characters SHALL be dropped.

#### Scenario: Rendered output strips ANSI and shows text

- **WHEN** a chunk containing ANSI color sequences and text arrives and the pane is drawn
- **THEN** the visible text appears without any escape sequences leaking into the output

#### Scenario: Bounded buffer drops oldest bytes

- **WHEN** more bytes arrive than the configured maximum buffer size
- **THEN** the internal buffer is trimmed to the trailing window and never exceeds the cap

#### Scenario: Only trailing lines are shown

- **WHEN** more lines arrive than the panel's inner height
- **THEN** the most recent lines are rendered in the viewport

#### Scenario: UTF-8 glyphs survive intact

- **WHEN** a chunk of multi-byte box-drawing glyphs is rendered
- **THEN** each glyph is preserved as a single rune rather than decomposed into raw bytes

#### Scenario: Zero or tiny rect is safe

- **WHEN** the pane is drawn with a zero-area or border-only rectangle
- **THEN** drawing completes without painting content and without error

### Requirement: Stream pane damage signal and redraw notification

A stream pane SHALL increment a monotonic damage counter each time a new non-empty chunk arrives, leaving the counter unchanged for empty chunks, so a surrounding tick loop can detect undrawn content. When a redraw callback is configured, it SHALL be invoked at least once per new non-empty chunk.

#### Scenario: Counter advances on new content

- **WHEN** a non-empty chunk arrives
- **THEN** the damage counter increases

#### Scenario: Empty chunk is a no-op

- **WHEN** an empty chunk arrives
- **THEN** the damage counter does not change

#### Scenario: Redraw callback fires on new content

- **WHEN** a redraw callback is configured and a non-empty chunk arrives
- **THEN** the callback is invoked at least once

### Requirement: Stream pane consumer lifecycle

A stream pane SHALL drain its source channel with a single consumer that exits cleanly when the pane is closed or the source channel is closed, while retaining and continuing to display already-buffered bytes after the source closes. Closing the pane SHALL be idempotent.

#### Scenario: Close stops the consumer

- **WHEN** the pane is closed
- **THEN** the consumer stops and no further chunks advance the damage counter

#### Scenario: Source close stops the consumer

- **WHEN** the source channel is closed
- **THEN** the consumer exits cleanly while previously buffered bytes remain displayable

#### Scenario: Close is idempotent

- **WHEN** the pane is closed more than once
- **THEN** no panic occurs

### Requirement: Stream pane input forwarding

A stream pane SHALL be read-only unless an input-back channel is wired. When an input-back channel is configured, the pane SHALL forward typed runes, mapped keys, and pasted text to that channel as encoded bytes; without an input-back channel its input handler SHALL be nil. Forwarding SHALL be non-blocking, dropping bytes when the channel is full and ignoring empty payloads.

#### Scenario: Keystrokes forward when wired

- **WHEN** an input-back channel is set and a rune or mapped key is entered
- **THEN** the corresponding bytes are sent to the input-back channel

#### Scenario: Pasted text forwards when wired

- **WHEN** an input-back channel is set and text is pasted
- **THEN** the pasted text bytes are sent to the input-back channel

#### Scenario: Read-only without input-back

- **WHEN** no input-back channel is configured
- **THEN** the input handler is nil and direct sends are no-ops

#### Scenario: Backpressure drops keystrokes

- **WHEN** the input-back channel is full and a keystroke is entered
- **THEN** the keystroke is dropped without blocking

### Requirement: Settings stream section connector lifecycle

A settings stream section SHALL, on focus, build a connector keyed by its scope and title, dial it within a bounded timeout, and on a successful dial send a focus signal and pump received bytes into the section's display channel using non-blocking sends that drop on backpressure. Opening a section whose key already has a connector SHALL defensively close the prior connector first. On blur, the section SHALL send a blur signal and close the connector, treating a section that was never opened as a no-op.

#### Scenario: Focus dials and pumps bytes

- **WHEN** a stream section gains focus
- **THEN** its connector is dialed, a focus signal is sent, and received bytes are delivered to the section's display channel

#### Scenario: Re-open replaces the prior connector

- **WHEN** a section is opened again while a connector already exists for its key
- **THEN** the prior connector is closed before the new one is registered

#### Scenario: Blur tears down the connector

- **WHEN** a previously opened section is blurred
- **THEN** a blur signal is sent and the connector is closed

#### Scenario: Blur without prior open is a no-op

- **WHEN** a section that was never opened is closed
- **THEN** no blur or close is invoked on any connector

### Requirement: TUI is a focused viewer only while in agent view

The TUI SHALL register itself as an active viewer of a session's PTY, under a
stable per-TUI viewer ID, when it enters the agent view for that task, reporting
the agent pane's current `(cols, rows)`. While in the agent view it SHALL update
the registered size when the pane dimensions change (debounced), rather than
applying an absolute PTY resize. When it leaves the agent view (returns to the task
list, switches to another task, or detaches the session) it SHALL release its
viewer entry so it no longer constrains the session's effective size. The TUI SHALL
NOT unconditionally force a resize on agent-view entry; size reconciliation happens
through the registry minimum.

#### Scenario: Entering agent view registers the pane size
- **WHEN** the user enters the agent view for a task
- **THEN** the TUI registers an active viewer carrying the agent pane's current dimensions

#### Scenario: Leaving agent view releases the claim
- **WHEN** the user returns to the task list or switches away from the task
- **THEN** the TUI removes its viewer entry and the session's effective size is recomputed without it

#### Scenario: Re-entry at the same size does not repaint
- **WHEN** the user re-enters the agent view and the recomputed minimum equals the current PTY size
- **THEN** no resize is issued and the agent is not forced to repaint

#### Scenario: Pane resize updates the registered size
- **WHEN** the agent pane dimensions change while in the agent view
- **THEN** the TUI updates its registered viewer size, and the PTY is resized only if the recomputed minimum changes

