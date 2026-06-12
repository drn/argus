# plugin-views delta: reconnect on daemon bounce

## MODIFIED Requirements

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

## ADDED Requirements

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
