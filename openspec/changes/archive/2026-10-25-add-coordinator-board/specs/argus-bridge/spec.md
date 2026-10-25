## MODIFIED Requirements

### Requirement: Cached composable Hera band

The mod SHALL render one truncate-end line above the prompt in the form `argus: <n> unread[ from <sender>] • budget <b>%[ • board N: <subject>] • <role status>[ • workers <w> working, <k> blocked][ • ⚠ bridge offline]`. The sender SHALL be the newest unread message's and SHALL appear only when unread is above zero. Budget SHALL appear only for coordinator bindings, as live context tokens over the configured budget rounded to a whole percent, with `?` when either is unknown. Role status SHALL be the bound role's Hera status, with distinct statuses joined by `/` when the task holds several bindings. The worker roll-up SHALL sum the snapshot's per-coordinator working and blocked worker counts, omit a zero count, and be absent when both are zero or no coordinator binding exists. The line SHALL NOT show context percent, role name, role kind or orchestrator, which the status line and the TUI rail already show.

The line SHALL be dimmed and SHALL use theme keys for every state color. The one raw color is the `argus:` label, `rgb(59,130,246)`, which matches the statusline's label exactly. The unread segment SHALL be bold `warning` when unread is above zero. Budget SHALL be uncolored below 75%, `warning` from 75%, and bold `warning` from 90%. Role status SHALL be `warning` when blocked, `success` when done and `error` when failed. The blocked worker count and the offline flag SHALL be `warning`.

When a refresh cannot reach the daemon (API files unreadable, a failed or non-OK response, or a timeout) the mod SHALL keep the last snapshot on screen and append the offline flag, and SHALL clear the flag on the next successful refresh. With no earlier snapshot the line stays hidden.

Rendering SHALL use a typed session atom and call next(e). It SHALL include beneath when another plugin drew it, and SHALL omit the engine's own empty component. The blank row between the band and the prompt box is the engine's fixed spacer between the transcript and the prompt box, present with or without a band, and lies outside the mod's tree. It SHALL yield to beneath whole while a survey holds the band. It SHALL hide its line when unbound, when no snapshot has ever loaded, when bridge.enabled=false or when bridge.band=false. Refresh SHALL occur after turn.complete, session.measure and on a slow timer without blocking hooks.

The coordinator binding SHALL expose `board_unread` and `board_subject`, the newest open unread item's subject ordered by update timestamp then id. The band SHALL show a separate `board N: <subject>` segment for each coordinator with unread board items, truncating the subject to at most 60 Unicode characters with an ellipsis. Zero or missing board counts SHALL omit the segment; board reads SHALL NOT acknowledge items or messages. Missing board tables in partial schemas SHALL behave as an empty board. Mail unread counts SHALL remain independent. Human decision-dismissal mail SHALL show `Aaron (board)` as sender in the band, inbox command and mail arrival toast. Board item arrivals SHALL NOT create toasts.

#### Scenario: Running band toggles without restart

- **WHEN** bridge.band changes off then on while the band is mounted
- **THEN** refresh removes then restores the line while preserving a plugin-drawn beneath

#### Scenario: Compact coordinator band

- **WHEN** a coordinator binding with status working has 0 unread, 250,000 context tokens, a 300,000 budget and no working or blocked workers
- **THEN** the band reads `argus: 0 unread • budget 83% • working`

#### Scenario: Unread sender and worker roll-up

- **WHEN** a blocked coordinator has 2 unread messages, the newest from `newest`, 270,000 tokens of a 300,000 budget, and 2 working and 1 blocked worker
- **THEN** the band reads `argus: 2 unread from newest • budget 90% • blocked • workers 2 working, 1 blocked` with the unread segment and budget bold `warning`, and the status and blocked count `warning`

#### Scenario: Zero worker counts omitted

- **WHEN** a coordinator's roll-up has 0 working and 3 blocked workers
- **THEN** the worker segment reads `workers 3 blocked`

#### Scenario: Daemon unreachable after a snapshot

- **WHEN** a band is showing and the next refresh gets a failed response
- **THEN** the band keeps the last values and appends `⚠ bridge offline`, and the flag clears on the next successful refresh

#### Scenario: Band tree holds only its own line

- **WHEN** no other plugin draws in the band and no survey is up
- **THEN** the band's tree holds only its own line, not the engine's empty component

#### Scenario: Board and mail remain separate

- **WHEN** a coordinator has 2 unread board items, newest subject `Choose a release`, and 1 unread message
- **THEN** the band includes `1 unread` and a distinct `board 2: Choose a release` segment, without acknowledging either source

#### Scenario: Empty or unavailable board

- **WHEN** no unread board items exist or the board table is absent
- **THEN** the snapshot and band retain the normal mail and worker information with no board segment

