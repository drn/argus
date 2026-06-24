# Terminal Rendering — delta for fix-bug-008

## ADDED Requirements

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
