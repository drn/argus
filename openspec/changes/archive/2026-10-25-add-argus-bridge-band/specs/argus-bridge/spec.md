## ADDED Requirements

### Requirement: Cached composable Hera band

The mod SHALL render one truncate-end line above the prompt in the form `argus: <n> unread[ from <sender>] • budget <b>% • <role status>[ • workers <w> working, <k> blocked][ • ⚠ bridge offline]`. The sender SHALL be the newest unread message's and SHALL appear only when unread is above zero. Budget SHALL appear only for coordinator bindings, as live context tokens over the configured budget rounded to a whole percent, with `?` when either is unknown. Role status SHALL be the bound role's Hera status, with distinct statuses joined by `/` when the task holds several bindings. The worker roll-up SHALL sum the snapshot's per-coordinator working and blocked worker counts, omit a zero count, and be absent when both are zero or no coordinator binding exists. The line SHALL NOT show context percent, role name, role kind or orchestrator, which the status line and the TUI rail already show.

The line SHALL use theme keys, never raw colors: the line `subtle`; the `argus:` label `ide`; the unread segment bold `warning` when unread is above zero; budget uncolored below 75%, `warning` from 75% and `claude` from 90%; role status `claude` when blocked, `success` when done and `error` when failed; the blocked worker count `claude`; the offline flag `warning`.

When a refresh cannot reach the daemon (API files unreadable, a failed or non-OK response, or a timeout) the mod SHALL keep the last snapshot on screen and append the offline flag, and SHALL clear the flag on the next successful refresh. With no earlier snapshot the line stays hidden.

Rendering SHALL use a typed session atom and call next(e). It SHALL include beneath when another plugin drew it, and SHALL omit the engine's own empty component (which adds a blank row between the band and the prompt box). It SHALL yield to beneath whole while a survey holds the band. It SHALL hide its line when unbound, when no snapshot has ever loaded, when bridge.enabled=false or when bridge.band=false. Refresh SHALL occur after turn.complete, session.measure and on a slow timer without blocking hooks.

#### Scenario: Running band toggles without restart

- **WHEN** bridge.band changes off then on while the band is mounted
- **THEN** refresh removes then restores the line while preserving a plugin-drawn beneath

#### Scenario: Compact coordinator band

- **WHEN** a coordinator binding with status working has 0 unread, 250,000 context tokens, a 300,000 budget and no working or blocked workers
- **THEN** the band reads `argus: 0 unread • budget 83% • working`

#### Scenario: Unread sender and worker roll-up

- **WHEN** a blocked coordinator has 2 unread messages, the newest from `newest`, 270,000 tokens of a 300,000 budget, and 2 working and 1 blocked worker
- **THEN** the band reads `argus: 2 unread from newest • budget 90% • blocked • workers 2 working, 1 blocked` with the unread segment bold `warning`, the budget, status and blocked count `claude`

#### Scenario: Zero worker counts omitted

- **WHEN** a coordinator's roll-up has 0 working and 3 blocked workers
- **THEN** the worker segment reads `workers 3 blocked`

#### Scenario: Daemon unreachable after a snapshot

- **WHEN** a band is showing and the next refresh gets a failed response
- **THEN** the band keeps the last values and appends `⚠ bridge offline`, and the flag clears on the next successful refresh

#### Scenario: No blank row above the prompt box

- **WHEN** no other plugin draws in the band and no survey is up
- **THEN** the band's tree holds only its own line, not the engine's empty component

### Requirement: Message arrival toasts

The mod SHALL toast new Hera/task message identities once, independent of model turns, when bridge.enabled and bridge.toasts are true. Initial unread mail SHALL establish a quiet baseline. Disabled toasts SHALL remain suppressed, including on live setting changes.

#### Scenario: Arrival while busy

- **WHEN** a new message appears during a turn and toasts are enabled
- **THEN** a toast announces its arrival once without submitting or acknowledging mail

### Requirement: Local Hera inspection commands

The mod SHALL register /hera-inbox and /hera-status during session.start and answer through command.run without a model turn. Inbox SHALL list unread sender, TLDR and age without acknowledgment. Status SHALL show role, kind, orchestrator, role status, binding, the band line, context percent and bridge liveness, and SHALL say when the daemon is unreachable and the values are the last snapshot. A session that never loaded a snapshot SHALL produce an explicit unavailable response.

#### Scenario: Inbox inspection is read-only

- **WHEN** /hera-inbox runs twice
- **THEN** both responses include the same unread mail and no delivery or read state changes
