# binary-coherence

## MODIFIED Requirements

### Requirement: Startup binary-skew detection and prompt

At startup the TUI SHALL detect binary skew against the daemon and the supervisor and present a blocking modal when skew is found. Daemon-staleness detection SHALL be performed only when the TUI connected to a pre-existing daemon (a daemon the TUI itself just auto-started cannot be stale). Supervisor-staleness detection SHALL be performed whenever a supervisor is present, regardless of how the daemon was started. The modal SHALL display the rich identity of whichever process is stale and offer the relevant restart action(s). Restarting the supervisor SHALL require a second explicit confirmation that names the number of running agents the restart will interrupt; declining the second confirmation SHALL leave the supervisor running. The modal SHALL auto-dismiss only when a single restart action (or none) was offered; when both the daemon and the supervisor are stale, resolving one restart action SHALL leave the modal open with the remaining action(s) until the user addresses or skips it.

#### Scenario: Supervisor checked on the auto-start path

- **WHEN** the TUI auto-started the daemon and the connected supervisor is stale
- **THEN** the skew modal SHALL be presented for the supervisor

#### Scenario: Daemon check gated on pre-existing connection

- **WHEN** the TUI auto-started the daemon
- **THEN** daemon-staleness SHALL NOT be evaluated for that daemon

#### Scenario: Rich identity in the modal

- **WHEN** the skew modal is presented
- **THEN** it SHALL display the commit SHA, dirty flag, and path (or hash fallback) of the stale process

#### Scenario: Supervisor restart double-confirm

- **WHEN** the user selects "restart supervisor" in the skew modal
- **THEN** a second confirmation SHALL be shown naming the count of agents that will be interrupted, and the supervisor SHALL be restarted only on an explicit second yes

#### Scenario: Declining the supervisor restart

- **WHEN** the user declines the second supervisor-restart confirmation
- **THEN** the supervisor SHALL be left running and no agents interrupted

#### Scenario: Restarting the daemon while the supervisor is also stale leaves the modal open

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart daemon"
- **THEN** the daemon restart SHALL fire and the skew modal SHALL remain open, now offering only "Restart supervisor" and "Skip"

#### Scenario: Restarting the supervisor resolves a pending daemon restart too

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart supervisor"
- **THEN** the skew modal SHALL dismiss in one step (the supervisor restart also bounces the daemon), proceeding straight to the supervisor's double-confirm

#### Scenario: Single restart action still auto-dismisses

- **WHEN** only the daemon or only the supervisor is stale and the user chooses the offered restart action
- **THEN** the skew modal SHALL dismiss immediately, unchanged from prior behavior
