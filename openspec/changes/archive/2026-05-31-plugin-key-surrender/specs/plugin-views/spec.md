## ADDED Requirements

### Requirement: Key surrender to a focused plugin view

While a plugin view has focus (the TUI is in plugin-view mode), argus SHALL forward every key event to the focused plugin and SHALL NOT reserve any key for its own navigation, with the single exception of the failsafe defined in the failsafe requirement. Esc, Ctrl+C, Ctrl+Q, `?`, and all modified arrows MUST reach the plugin.

#### Scenario: Esc reaches the plugin

- **WHEN** a plugin view has focus and the user presses Esc
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
