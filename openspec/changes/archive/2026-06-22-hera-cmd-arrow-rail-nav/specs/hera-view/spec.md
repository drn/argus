## ADDED Requirements

### Requirement: Cmd+Up / Cmd+Down move rail selection without changing pane focus

The Hera view SHALL intercept `KeyUp` / `KeyDown` events carrying the `ModCtrl|ModAlt` modifier (the mod-7 encoding iTerm2 maps `Cmd+↑` / `Cmd+↓` onto) in `HeraPage.InputHandler` BEFORE forwarding any key to a focused content pane.

When intercepted the handler SHALL call `rail.CursorUp()` or `rail.CursorDown()` and return, consuming the event so the mod-7 escape sequence never reaches the focused pane's PTY.

The `FocusMachine` state (rail / coordinator pane / agent pane) SHALL remain unchanged.

The binding SHALL appear in the Hera rail section of the help overlay (`?`) and in the README Reference keybinding table.

#### Scenario: Cmd+Down from coordinator pane moves rail cursor

- **WHEN** the Hera tab is active and focus is on the coordinator pane
- **WHEN** the user presses `Cmd+Down` (KeyDown + ModCtrl|ModAlt)
- **THEN** the rail cursor advances to the next selectable row
- **THEN** the focus machine state remains FocusCoord

#### Scenario: Cmd+Up from agent pane moves rail cursor

- **WHEN** the Hera tab is active and focus is on the agent pane
- **WHEN** the user presses `Cmd+Up` (KeyUp + ModCtrl|ModAlt)
- **THEN** the rail cursor retreats to the previous selectable row
- **THEN** the focus machine state remains FocusAgent

#### Scenario: Keystroke is not forwarded to the PTY

- **WHEN** the user presses `Cmd+Down` while focused on a content pane
- **THEN** the mod-7 byte sequence `\x1b[1;7B` is NOT written to the pane session's input
