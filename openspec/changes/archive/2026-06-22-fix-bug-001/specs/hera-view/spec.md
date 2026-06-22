# Hera View

## ADDED Requirements

### Requirement: Enter in a focused pane revives its dead/suspended session (area 6)

The system SHALL, when `Enter` is pressed while a focused content pane (the
coordinator or agent terminal) has no live session attached — the state behind
the `Session not running - press Enter to start` overlay (session nil or
`!Alive()`) — fire that pane's reattach callback to revive the pane's bound task,
performing exactly what the overlay promises. The reattach SHALL route through the
SAME `OnReattach` → `App.heraReattach` path the rail's `Enter` uses (a dead session
is restarted via `startSession`; a live coordinator stays navigate-only). The
agent pane SHALL target the selected worker's task; the coordinator pane SHALL
target the orchestrator's coordinator task it is showing.

For EVERY other case the pane SHALL leave key handling unchanged: a non-`Enter`
key, or `Enter` while the session IS alive, SHALL fall through so live PTY input
reaches the agent. The reattach callback SHALL be nil-guarded so a consumer that
does not wire it (the task page, which mounts the same widget) keeps the pane
inert — its live-session input continues to flow through its own routing
(`handleAgentKey`), never through the pane's `InputHandler`.

This introduces NO new keybinding — `Enter`-to-revive is already the rail's
documented behavior; this extends it to the focused pane, so the help overlay and
README key tables are unchanged.

Derived from: `internal/tui/terminal/terminalpane.go` (`OnReattach` field, `InputHandler` Enter-when-not-alive gate), `internal/tui/hera/panes.go` (`forwardKey` dead-branch routes to the pane `InputHandler`, `bindPane` wires `OnReattach`, `reattachPane` per-pane target), `internal/tui/heraactions.go` (`heraReattach`).

#### Scenario: Enter in a focused dead-session pane revives it

- **WHEN** a content pane is focused, its session is nil or `!Alive()` (the "Session not running" overlay is showing), and the user presses `Enter`
- **THEN** the pane's reattach callback fires and the App revives the pane's bound task (a dead session restarts via `startSession`)

#### Scenario: Enter in a focused live pane reaches the agent, not the revive path

- **WHEN** a content pane is focused, its session is alive, and the user presses `Enter`
- **THEN** the reattach callback does NOT fire and `Enter` is forwarded to the agent PTY

#### Scenario: A non-Enter key in a focused dead-session pane does not revive

- **WHEN** a content pane is focused with no live session and a key other than `Enter` is pressed
- **THEN** the reattach callback does NOT fire (the key is handled exactly as before)

#### Scenario: The task page keeps the pane inert

- **WHEN** the same `TerminalPane` widget is mounted by the task page, which does not wire `OnReattach`, and `Enter` is pressed with no live session
- **THEN** the nil-guarded callback makes the pane a no-op, unchanged from prior behavior
