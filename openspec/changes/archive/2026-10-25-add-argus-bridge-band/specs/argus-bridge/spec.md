## ADDED Requirements

### Requirement: Cached composable Hera band

The mod SHALL render one truncate-end line above the prompt showing role name/kind, orchestrator, unread count, context percent and coordinator configured budget. Rendering SHALL use a typed session atom, call next(e) and include beneath. It SHALL hide its line when unbound, unavailable, bridge.enabled=false or bridge.band=false. Refresh SHALL occur after turn.complete, session.measure and on a slow timer without blocking hooks.

#### Scenario: Running band toggles without restart

- **WHEN** bridge.band changes off then on while the band is mounted
- **THEN** refresh removes then restores the line while preserving beneath

### Requirement: Message arrival toasts

The mod SHALL toast new Hera/task message identities once, independent of model turns, when bridge.enabled and bridge.toasts are true. Initial unread mail SHALL establish a quiet baseline. Disabled toasts SHALL remain suppressed, including on live setting changes.

#### Scenario: Arrival while busy

- **WHEN** a new message appears during a turn and toasts are enabled
- **THEN** a toast announces its arrival once without submitting or acknowledging mail

### Requirement: Local Hera inspection commands

The mod SHALL register /hera-inbox and /hera-status during session.start and answer through command.run without a model turn. Inbox SHALL list unread sender, TLDR and age without acknowledgment. Status SHALL show role, kind, orchestrator, role status, binding, unread count, context/budget and bridge liveness. Unavailable state SHALL produce an explicit local response.

#### Scenario: Inbox inspection is read-only

- **WHEN** /hera-inbox runs twice
- **THEN** both responses include the same unread mail and no delivery or read state changes
