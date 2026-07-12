## ADDED Requirements

### Requirement: Terminal input Ctrl+Z guard

The system SHALL remove the Ctrl+Z byte (`0x1A`, ASCII SUB) from all keyboard
input captured in the terminal surface before forwarding it to the daemon's
`POST /api/tasks/{id}/input` endpoint. All other bytes — including other control
characters such as Ctrl+C (`0x03`), Ctrl+Y (`0x19`), and ESC (`0x1B`) — SHALL be
forwarded unchanged and in their original order.

A literal Ctrl+Z keypress SHALL be swallowed (nothing is forwarded); the app
SHALL NOT remap Ctrl+Z to another action, because the SwiftUI surface has no
terminal pane-zoom / fullscreen affordance analogous to the TUI's Ctrl+Z remap.

This guard exists because Claude Code's CLI runs its own background-session
supervisor: a literal Ctrl+Z byte reaching it reparents the agent session out of
argus's process tree permanently, orphaning it. The TUI already guards the same
footgun by never forwarding Ctrl+Z to the PTY; this requirement establishes
parity for the macOS app.

The decision logic SHALL be a pure, dependency-free helper
(`ArgusKit.TerminalInput.sanitize`) so it is unit-testable without SwiftTerm or
AppKit, and the terminal input delegate (`ArgusMac.TerminalCoordinator.send`)
SHALL call it at the single outbound chokepoint and SHALL log when a byte is
dropped.

#### Scenario: A lone Ctrl+Z keypress is swallowed

- **WHEN** the user presses Ctrl+Z while the terminal has focus and SwiftTerm
  delivers a `0x1A` byte to the input delegate
- **THEN** no bytes are forwarded to `POST /input` (the keystroke is dropped) and
  the agent session is neither suspended nor reparented out of argus's process
  tree

#### Scenario: Ctrl+Z embedded in a payload is stripped, the rest forwarded

- **WHEN** an outbound input payload contains a `0x1A` byte among other bytes
- **THEN** the `0x1A` byte(s) are removed and the remaining bytes are forwarded
  to `POST /input` unchanged and in their original order

#### Scenario: Other control bytes reach the PTY untouched

- **WHEN** the user presses a control key other than Ctrl+Z (e.g. Ctrl+C `0x03`,
  Ctrl+Y `0x19`, or ESC `0x1B`)
- **THEN** the corresponding byte is forwarded to `POST /input` unchanged — the
  guard is surgical to Ctrl+Z, not a blanket control-byte filter
