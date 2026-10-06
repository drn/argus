## ADDED Requirements

### Requirement: Hera pane respawn uses the pane's PTY size

When a session is revived, size-drift-kicked, or restarted while the Hera tab is active and a Hera terminal pane is bound to that task, the new session SHALL be spawned at that pane's PTY size rather than the host terminal's size. When no laid-out Hera pane shows the task, or the Hera tab is not active, the host-terminal size SHALL be used.

#### Scenario: Revive from the Hera tab
- **WHEN** the operator revives a stuck worker shown in a 90-column Hera agent pane on a 212-column terminal
- **THEN** the resumed session starts at 90 columns, so its re-emitted history is committed at the width the pane displays

#### Scenario: Revive with no bound pane
- **WHEN** a session is started for a task no Hera pane is showing
- **THEN** it starts at the host-terminal size as before
