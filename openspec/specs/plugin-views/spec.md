# plugin-views Specification

## Purpose
TBD - created by archiving change plugin-key-surrender. Update Purpose after archive.
## Requirements
### Requirement: Key surrender to a focused plugin view

While a plugin view has focus AND its connection to the plugin is live (the TUI is in plugin-view mode and not reconnecting), argus SHALL forward every key event to the focused plugin and SHALL NOT reserve any key for its own navigation, with the single exception of the failsafe defined in the failsafe requirement. Esc, Ctrl+C, `?`, and all modified arrows MUST reach the plugin. While the view is in the reconnecting state (the connection has dropped and argus is retrying), Esc-to-exit applies as defined in the reconnect requirement.

#### Scenario: Esc reaches the plugin

- **WHEN** a plugin view has focus, its connection is live, and the user presses Esc
- **THEN** argus forwards Esc to the plugin (it does not exit the view or return focus to argus)

#### Scenario: Ctrl+C reaches the plugin instead of quitting argus

- **WHEN** a plugin view has focus and the user presses Ctrl+C
- **THEN** argus forwards Ctrl+C to the plugin and does not quit

#### Scenario: question mark reaches the plugin

- **WHEN** a plugin view has focus and the user presses `?`
- **THEN** argus forwards `?` to the plugin and does not open argus's own help

#### Scenario: argus navigation keys are surrendered

- **WHEN** a plugin view has focus and the user presses a key argus would otherwise use for its own navigation (e.g. a tab-switch number or a focus-rail arrow)
- **THEN** argus forwards that key to the plugin and performs no argus navigation

### Requirement: Faithful key encoding

Keystrokes forwarded to a plugin SHALL be encoded as the standard xterm byte sequences a terminal application expects, produced by a single shared encoder used by every keystroke-forwarding call site. Modified arrows, Home, and End MUST use the `CSI 1 ; <mod> <final>` form where `<mod>` is `1 + Shift(1) + Alt(2) + Ctrl(4)`. Every key sequence the prior encoders emitted MUST remain byte-identical.

#### Scenario: Ctrl+Right encodes to the modified-arrow sequence

- **WHEN** a Ctrl+Right key event is forwarded to a plugin
- **THEN** the plugin receives `\x1b[1;5C`

#### Scenario: Ctrl+Alt+Right round-trips the Cmd+arrow sequence

- **WHEN** a Ctrl+Alt+Right key event is forwarded to a plugin (the form iTerm2 emits for Cmd+Right)
- **THEN** the plugin receives `\x1b[1;7C`

#### Scenario: Shift and Alt modified arrows use the same form

- **WHEN** a Shift+Right or Alt+Right key event is forwarded to a plugin
- **THEN** the plugin receives `\x1b[1;2C` or `\x1b[1;3C` respectively

#### Scenario: plain navigation keys are forwarded

- **WHEN** an unmodified arrow, Home, End, PgUp, or PgDn is forwarded to a plugin
- **THEN** the plugin receives that key's standard sequence (it is not dropped)

#### Scenario: previously-mapped keys are unchanged

- **WHEN** any key that the prior encoders already mapped (rune, Enter, Tab, Backspace, Delete, Escape, Ctrl+letter) is encoded by the shared encoder
- **THEN** the emitted bytes are identical to what the prior encoders produced

### Requirement: Plugin-initiated release

A plugin SHALL be able to hand control back to argus by sending a `{"type":"release"}` JSON control frame over the plugin-view WebSocket. On receipt, argus MUST tear down the active plugin view (blur, close the connection) and return focus to argus.

#### Scenario: release returns the ball to argus

- **WHEN** the active plugin sends `{"type":"release"}`
- **THEN** argus deactivates the plugin view and returns focus to the task list

### Requirement: Double-Ctrl+Q failsafe

Argus SHALL forward a single Ctrl+Q to the focused plugin, but two Ctrl+Q presses within the failsafe window (approximately 400 ms) MUST force-return control to argus regardless of whether the plugin cooperates. The failsafe is the only key argus reserves while a plugin has the ball.

#### Scenario: a single Ctrl+Q is forwarded

- **WHEN** a plugin view has focus and the user presses Ctrl+Q once with no second press inside the window
- **THEN** argus forwards Ctrl+Q to the plugin and does not return control to argus

#### Scenario: a fast double Ctrl+Q force-returns

- **WHEN** a plugin view has focus and the user presses Ctrl+Q twice within the failsafe window
- **THEN** argus deactivates the plugin view and returns focus to argus, even if the plugin never sent a release

### Requirement: Plugin hotkey dictionary drives the bottom bar

A plugin SHALL be able to push a `{"type":"hotkeys","items":[{"key":..,"label":..,"bar":bool}]}` control frame describing its currently-active hotkeys. While a plugin has the ball, argus's bottom bar MUST render the `bar:true` subset and MUST always render a reserved "return to argus" exit hint that the plugin's items cannot suppress or displace. When the plugin releases the ball, argus MUST revert to showing its own hints.

#### Scenario: bar-flagged hotkeys render in the bottom bar

- **WHEN** a plugin pushes a hotkey dictionary while it has the ball
- **THEN** argus renders the items flagged `bar:true` in the bottom bar

#### Scenario: the exit hint is always present

- **WHEN** a plugin has the ball, regardless of what hotkeys it pushes
- **THEN** argus renders a reserved "return to argus" exit hint that the plugin's items cannot occupy or push off-screen

#### Scenario: the bar updates on re-push

- **WHEN** the plugin pushes an updated hotkey dictionary (e.g. after its internal focus changes)
- **THEN** the bottom bar updates to reflect the new bar-flagged items

#### Scenario: no dictionary falls back to an affordance

- **WHEN** a plugin has the ball but has pushed no hotkey dictionary
- **THEN** the bottom bar shows a "<plugin> has the keyboard" affordance plus the reserved exit hint

#### Scenario: argus hints return after release

- **WHEN** the plugin releases the ball (or the failsafe fires)
- **THEN** the bottom bar shows argus's own tab hints, not plugin hotkeys

### Requirement: Plugin-triggered help overlay

A plugin SHALL be able to request the help overlay by sending a `{"type":"help"}` control frame. Argus MUST render the plugin's full pushed hotkey dictionary (every item, ignoring the `bar` flag) in its help overlay, showing only the plugin's hotkeys and not argus's bindings. Argus MUST NOT reserve `?` itself; the plugin owns `?` and decides when to request help.

#### Scenario: help request renders the full dictionary

- **WHEN** the active plugin sends `{"type":"help"}` after pushing a hotkey dictionary
- **THEN** argus pops the help overlay listing every hotkey in the dictionary

#### Scenario: overlay shows only plugin hotkeys

- **WHEN** the help overlay is shown while a plugin has the ball
- **THEN** the overlay lists the plugin's hotkeys and does not list argus's own bindings

### Requirement: Robust control-frame handling

Argus SHALL accept plugin → argus control frames over the plugin-view WebSocket and dispatch known types (`release`, `hotkeys`, `help`). Unknown or malformed control frames MUST be ignored without disrupting the binary ANSI byte stream or crashing the read pump.

#### Scenario: unknown control frame is ignored

- **WHEN** the plugin sends a control frame with an unrecognized `type`
- **THEN** argus ignores it and continues processing subsequent frames normally

#### Scenario: malformed control frame does not crash the pump

- **WHEN** the plugin sends a malformed (non-JSON) text frame
- **THEN** argus ignores it without panicking and the binary ANSI stream continues to render

#### Scenario: oversized hotkey dictionary is bounded

- **WHEN** a plugin pushes a hotkey dictionary whose item count or whose individual `key`/`label` rune lengths exceed argus's caps
- **THEN** argus MUST store only a bounded subset (item count clamped, over-long `key`/`label` truncated rather than dropped) so that both the bottom bar and the help overlay render without unbounded memory or CPU use

### Requirement: Resize envelope reflects the real viewport

Argus SHALL compute the resize envelope (`{"type":"resize","cols":N,"rows":M}`) for a plugin view from the pane's post-layout rect. Argus MUST NOT send an envelope derived from a pane rect that has never been laid out (the tview Box default), and MUST NOT read the pane rect from outside the tview goroutine. When no laid-out rect exists yet, the viewport SHALL derive from the screen size minus fixed chrome (header, status bar, pane border).

#### Scenario: first envelope is post-layout

- **WHEN** a plugin view is activated and the WebSocket dial completes before the pane's first layout pass
- **THEN** argus defers the resize envelope until after the pane has been drawn, and the envelope matches the pane's real inner size (never the 13x8 un-laid-out Box default)

#### Scenario: pre-layout viewport falls back to screen-derived size

- **WHEN** the viewport size is computed before the pane has been laid out
- **THEN** the result derives from the screen size minus fixed chrome, not from the un-laid-out pane rect

### Requirement: Resize envelope reconciliation

Argus SHALL track the cols/rows of the last resize envelope delivered on the active plugin connection and SHALL re-send the envelope whenever the computed viewport differs from the last-sent value — on any draw, not only when the terminal size changes. A failed send MUST NOT update the last-sent value, so the envelope is retried on a subsequent draw.

#### Scenario: corrected size is re-sent without a terminal resize

- **WHEN** the computed viewport for the active plugin view changes (e.g. the first real layout pass lands) while the terminal size stays the same
- **THEN** argus sends a fresh resize envelope with the new cols/rows

#### Scenario: unchanged size is not re-sent

- **WHEN** draws occur while the computed viewport equals the last envelope sent
- **THEN** argus does not send duplicate resize envelopes

#### Scenario: re-activation re-sends the current size

- **WHEN** the user re-activates the already-active plugin view
- **THEN** argus re-sends the current computed viewport as a recovery hint, even though the size has not changed

### Requirement: Reconnect on unexpected disconnect

When the WebSocket connection to an active plugin view drops unexpectedly (the plugin daemon exits or the socket errors), argus SHALL detect the drop, display an app-level "reconnecting" overlay over the (now frozen) plugin pane, and retry dialing the view's callback URL with capped backoff until the connection is re-established or the user exits. An explicit teardown (deactivate, double-Ctrl+Q failsafe, or Esc while reconnecting) MUST NOT be treated as an unexpected disconnect and MUST NOT start a reconnect.

#### Scenario: dropped connection shows the reconnecting overlay

- **WHEN** the plugin daemon dies while its plugin view is active
- **THEN** argus shows a "Reconnecting to {title}…" overlay over the frozen pane and begins retrying the dial with backoff

#### Scenario: explicit exit does not start a reconnect

- **WHEN** the user closes the plugin view via the double-Ctrl+Q failsafe (or the connection is closed by argus during deactivate)
- **THEN** argus returns to the task list and does NOT show a reconnecting overlay or start a redial loop

#### Scenario: retry continues until the daemon returns

- **WHEN** the dial keeps failing because the daemon has not finished restarting
- **THEN** argus keeps retrying with capped backoff and leaves the reconnecting overlay up; it does not give up on its own

#### Scenario: exit keys are always visible on the overlay

- **WHEN** the reconnecting overlay is shown (at any elapsed time)
- **THEN** the overlay renders the keys that exit the view (Esc, or double-Ctrl+Q)

#### Scenario: prolonged outage escalates the headline message

- **WHEN** reconnection has been retrying for an extended period (about two minutes)
- **THEN** the overlay's headline message changes from "Reconnecting…" to a "still trying" message (the exit-key hint remains visible throughout)

#### Scenario: the plugin's bottom-bar hotkeys are not shown while reconnecting

- **WHEN** a plugin view is in the reconnecting state
- **THEN** argus does not render the plugin's pushed hotkey hints in the bottom bar (the plugin no longer has the keyboard); the exit affordance is surfaced by the reconnecting overlay, and the bar's hints are restored on a successful resume

### Requirement: Seamless resume after reconnect

When a reconnect dial succeeds, argus SHALL wire a fresh connection to the same plugin pane, dismiss the reconnecting overlay, and perform the same initial resize-then-focus handshake as a new connection — sending the current post-layout viewport as a resize envelope followed by a focus envelope — so the plugin re-renders at the correct size without any manual quit/relaunch. Because a warm-restarted plugin daemon may render its first frame before it applies that initial resize, argus SHALL re-send the resize envelope once more a short time after the resume so the plugin receives a resize after it has settled and re-renders at the correct size.

#### Scenario: resume re-sends resize and focus

- **WHEN** a reconnect dial succeeds for an active plugin view whose pane is already laid out
- **THEN** argus dismisses the overlay and sends a fresh resize envelope matching the pane's real inner rect, followed by a focus envelope

#### Scenario: resume does not reuse stale envelope state

- **WHEN** a reconnect succeeds
- **THEN** the last-sent size and focus-sent state are reset so the first envelope on the new connection is sent even though the viewport size did not change

#### Scenario: resume re-sends the resize after a short delay

- **WHEN** a reconnect dial succeeds and the resume completes
- **THEN** argus re-sends the current resize envelope once more a short time later (defeating a warm-restarted plugin that rendered before applying the first resize), with the real viewport dimensions — never a tiny or zero size

### Requirement: Exit while reconnecting

While a plugin view is in the reconnecting state, argus SHALL allow the user to exit to the task list with a single Esc, in addition to the double-Ctrl+Q failsafe. Exiting MUST cancel the redial loop and remove the reconnecting overlay.

#### Scenario: Esc exits during reconnect

- **WHEN** a plugin view is showing the reconnecting overlay and the user presses Esc
- **THEN** argus cancels the redial loop, removes the overlay, and returns to the task list

#### Scenario: double-Ctrl+Q exits during reconnect

- **WHEN** a plugin view is showing the reconnecting overlay and the user presses Ctrl+Q twice within the failsafe window
- **THEN** argus cancels the redial loop, removes the overlay, and returns to the task list

### Requirement: OSC-8 hyperlink rendering parity

The plugin-view terminal pane SHALL render OSC 8 hyperlinks emitted by a
plugin's PTY output as real terminal hyperlinks, at parity with the default
agent terminal pane. When the VT emulator parses a cell carrying a non-empty
link URL, the pane's cell-style mapper MUST attach that URL to the
corresponding tcell cell style so the outer terminal receives the hyperlink
escape sequence as part of the normal draw cycle. A cell with no link URL
MUST NOT have a URL attached.

#### Scenario: A plugin-printed link renders as a real hyperlink

- **WHEN** a plugin's PTY output contains an OSC 8 hyperlink (e.g. a
  clickable PR reference or "view artifact" link) and the plugin view is
  drawn
- **THEN** the cell carrying that link's text is painted with a tcell style
  whose URL matches the link, so the user's real terminal renders it as a
  clickable hyperlink

#### Scenario: Plain text is unaffected

- **WHEN** a cell has no OSC 8 link associated with it
- **THEN** the mapped tcell style carries no URL

