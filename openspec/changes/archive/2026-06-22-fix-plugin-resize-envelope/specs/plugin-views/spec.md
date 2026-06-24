# plugin-views delta: resize envelope robustness

## ADDED Requirements

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
