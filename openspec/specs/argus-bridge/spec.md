# Argus bridge specification

## Purpose

Define how live Claude bridge reports and terminal inference combine into consistent session verdicts across daemon clients.
## Requirements
### Requirement: Uncertain permission input is corroborated per field

A live bridge SHALL remain the source throughout pending-or-running permission intervals. Display consumers SHALL preserve bridge busy state and resolve needs-input as bridge input AND the raw scraper needs-input for uncertain permissions. Exact permission, question and plan input SHALL use bridge state directly. Missing permission confidence SHALL be treated as uncertain. Source logs SHALL fire only for real liveness or authority transitions, never for an uncertain field alone. REST status, daemon-connected TUI, web, macOS and push SHALL apply the same display rule. The separate shadow permission diagnostic bucket SHALL compare raw bridge needs-input against scraper input. Delivery routing and claim-settlement evidence SHALL retain raw bridge needs-input as a conservative safety gate.

#### Scenario: Hook-approved long tool execution

- **WHEN** a busy live bridge reports a pending-or-running permission and the scraper reports no input request
- **THEN** it remains busy without displayed needs-input and without a source transition

#### Scenario: Real permission dialog

- **WHEN** both a live uncertain permission and the scraper report needs-input
- **THEN** displayed needs-input is true and the session is quiescent while bridge busy remains unchanged

#### Scenario: Exact permission

- **WHEN** a live bridge reports an exact permission request and the scraper disagrees
- **THEN** displayed needs-input is true immediately

#### Scenario: Prior exact input must not corroborate a later uncertain permission

- **WHEN** an exact dialog was published and a later uncertain permission arrives without raw scraper input
- **THEN** the previous published verdict does not corroborate the uncertain permission

#### Scenario: Telemetry hook unavailable

- **WHEN** the engine refuses first-party telemetry observation for user mods
- **THEN** the mod retains its existing grace-period pending-or-running fallback

#### Scenario: Raw permission disagreement remains observable

- **WHEN** raw bridge permission input is true and scraper input is false beyond the shadow settling period
- **THEN** disagreement counts under needs_input_permission while the headline needs_input counters remain unaffected

#### Scenario: Delivery blocks despite display corroboration clearing input

- **WHEN** the bridge is idle with raw uncertain permission input true and scraper input false
- **THEN** delivery is blocked and a fresh heartbeat does not establish settled absence for a claim

### Requirement: Bridge mod is embedded and materialized content-addressed
The system SHALL embed the `argus-bridge` Claude Code mod in the argus binary and materialize it to a content-addressed directory under `~/.argus/` before it is loaded, so a running session never observes a half-written or mutated mod directory.

#### Scenario: First materialization
- **WHEN** a Claude session is launched and the mod has not been materialized for this binary's content
- **THEN** the embedded files are written to `~/.argus/cache/bridge-mod/<content-hash>/argus-bridge/` and that directory path is returned

#### Scenario: Idempotent
- **WHEN** materialization runs again with unchanged embedded content
- **THEN** the same path is returned and no file is rewritten

#### Scenario: Concurrent first launches
- **WHEN** many launches materialize the same content at once
- **THEN** every launch is handed a directory containing every mod file, and no launch removes a directory another was handed

#### Scenario: Sandbox-writable location
- **WHEN** a session runs inside the argus sandbox
- **THEN** the mod directory sits under `~/.argus/cache`, the only part of `~/.argus` the sandbox may write

#### Scenario: Never under go test
- **WHEN** the code runs inside a Go test binary
- **THEN** materialization returns an empty path without touching the real `~/.argus`

### Requirement: Bridge mod is inert without a task id
The mod SHALL register no reporting behavior unless the `ARGUS_TASK_ID` environment variable is set in the Claude session.

#### Scenario: Plain Claude session
- **WHEN** the mod loads in a session where `ARGUS_TASK_ID` is unset
- **THEN** it posts nothing and starts no timers

### Requirement: Bridge mod reports session lifecycle and state events
The mod SHALL report structured events to the daemon, each carrying a monotonic `seq` within the mod's `boot` id, a timestamp and the current session id: `session.start` (session id, model, cwd), `turn.start`, `turn.complete`, `needs_input`, `cleared`, `session.measure` (context tokens/percent/window, cost, rate limits) and a periodic `heartbeat` of about 15 seconds. `turn.complete` SHALL carry the `turn_id` of its `turn.start`. Only main-loop `turn.start`/`turn.complete` SHALL be reported (those carrying an `agentId` SHALL be ignored for busy/idle); needs-input raised inside a subagent SHALL still be reported. `session.measure` SHALL be sent at most once every 5 seconds (the latest held and sent on turn end or heartbeat), and a measurement identical to the last one sent SHALL be dropped.

#### Scenario: Busy and idle
- **WHEN** a main-loop turn starts and later completes
- **THEN** a `turn.start` and a `turn.complete` event are posted in order with increasing `seq`

#### Scenario: Subagent turns are ignored for busy/idle
- **WHEN** a turn event carries an `agentId`
- **THEN** no `turn.start` or `turn.complete` is posted for it

#### Scenario: Subagent dialogs are reported
- **WHEN** a subagent's `AskUserQuestion` or an `ask`-verdict tool call stays pending
- **THEN** `needs_input` is posted as for a main-loop call

#### Scenario: Measurement flood
- **WHEN** `session.measure` fires many times within five seconds
- **THEN** the first is sent, the rest are held, and only the latest is sent later (when it differs from what was last sent)

#### Scenario: Session id changes after /clear
- **WHEN** a `turn.start` fires and `$.session.id()` differs from the last reported id
- **THEN** a `session.start` carrying the new id is posted before the `turn.start`

#### Scenario: Measurements
- **WHEN** `session.measure` fires
- **THEN** a `session.measure` event is posted with context tokens, percent and window, cost in USD when present, and every rate-limit window with its percent used and reset time

### Requirement: Bridge mod derives needs-input from pending tool calls
The mod SHALL report `needs_input` when a user dialog is pending and `cleared` when none remain. `AskUserQuestion` and `ExitPlanMode` calls SHALL be pending from `tool.call` pre to post. For any other tool, a `tool.check` verdict of `ask` whose `tool.call` is still pending after a grace period of about 1.5 seconds SHALL count as pending until `tool.call` post. The mod SHALL NOT rely on `classic.PermissionRequest` or `classic.Notification`.

#### Scenario: Question dialog
- **WHEN** `AskUserQuestion` begins and has not completed
- **THEN** `needs_input` with kind `question` is posted, and `cleared` is posted when the call completes

#### Scenario: Auto-approved permission ask
- **WHEN** `tool.check` returns `ask` and the `tool.call` completes within the grace period
- **THEN** neither `needs_input` nor `cleared` is posted

#### Scenario: Unanswered permission dialog
- **WHEN** `tool.check` returns `ask` and the `tool.call` is still pending after the grace period
- **THEN** `needs_input` with kind `permission` and confidence `pending-or-running` is posted, and `cleared` is posted when the call completes

#### Scenario: Exact dialogs
- **WHEN** `needs_input` is posted for `AskUserQuestion` or `ExitPlanMode`
- **THEN** its kind is `question` or `plan` and its confidence is `exact`

### Requirement: Bridge mod fails open and never waits on the network
The mod SHALL NOT throw out of any hook and SHALL NOT delay or break the session when the daemon is unreachable or unresponsive: no hook SHALL wait on event delivery, which runs as a background drain. It SHALL discover the daemon by reading `<HOME>/.argus/api-token` and `<HOME>/.argus/api-port`, re-reading the port after any connection failure, and SHALL buffer at most a bounded number of events for brief retry, dropping the oldest beyond the bound.

#### Scenario: Daemon down
- **WHEN** the daemon is unreachable or the token/port files are missing
- **THEN** the session continues normally and events are dropped after the bounded buffer fills

#### Scenario: Relocated data directory
- **WHEN** `ARGUS_BRIDGE_DIR` is set
- **THEN** the token and port are read from that directory instead of `<HOME>/.argus`

#### Scenario: Unresponsive daemon
- **WHEN** the daemon accepts the connection but never answers
- **THEN** every hook still returns immediately and events stay buffered

#### Scenario: Stale port
- **WHEN** a post to the cached port returns a non-2xx status other than 401
- **THEN** the cached port is forgotten and re-read from the port file before the next attempt

#### Scenario: Daemon restarted on a new port
- **WHEN** a post fails to connect and the port file now names a different port
- **THEN** the retry targets the new port

### Requirement: Daemon keeps the latest bridge state per task
The daemon SHALL accept bridge events for a task, drop duplicate or out-of-order events within one `boot` id, accept a new `boot` id as a fresh baseline, and keep in memory the latest bridge state of each task: session id, model, busy/idle, pending needs-input, latest measurement, last event time and last heartbeat time. A bridge is live while its newest event is younger than 45 seconds.

#### Scenario: Out-of-order event
- **WHEN** an event arrives with a `seq` less than or equal to the last accepted `seq` of the same `boot`
- **THEN** it is ignored

#### Scenario: Reloaded mod
- **WHEN** an event arrives with a different `boot` id and a lower `seq`
- **THEN** it is accepted and becomes the new baseline, and the task's busy and needs-input state are reset

#### Scenario: Silent task
- **WHEN** a task's state has had no event for over an hour
- **THEN** it is pruned from memory

### Requirement: Shadow comparison against screen-reading verdicts
For each running task with a live bridge, the daemon SHALL compare its screen-reading verdicts against the bridge state on every watcher tick, for two signals: idle (bridge idle meaning turn idle or needs-input pending) and needs-input. A disagreement SHALL be counted and logged only after it persists past a settle window (default 10 seconds); shorter disagreements SHALL be counted as settling. A task without a live bridge SHALL be counted as `no_bridge` and never as a disagreement.

#### Scenario: Agreement
- **WHEN** the scraper and the bridge agree on a signal for a task
- **THEN** the signal's `agree` counter increments

#### Scenario: Transient lag
- **WHEN** the scraper and the bridge disagree for less than the settle window and then agree
- **THEN** no disagreement is logged and the ticks count as `settling`

#### Scenario: Persistent disagreement
- **WHEN** they disagree continuously past the settle window
- **THEN** one `[bridge] disagree` uxlog line carries signal, task id and both values, `disagree` increments each tick it persists, and one `[bridge] resolved` line records the total duration when they agree again

#### Scenario: No bridge
- **WHEN** the task has no bridge events or the bridge is not live
- **THEN** the `no_bridge` counter increments and nothing is logged

#### Scenario: Permission-kind dialog counted apart
- **WHEN** the bridge holds a permission-kind needs-input and the scraper disagrees
- **THEN** the disagreement counts under `needs_input_permission`, not `needs_input`

### Requirement: Shadow mode changes no behavior
In phase 1 the daemon SHALL NOT use bridge state to change any verdict, status, notification, delivery or session action.

#### Scenario: Disagreeing bridge
- **WHEN** the bridge says idle while the scraper says busy
- **THEN** the scraper verdict still drives every existing consumer

### Requirement: Bridge authority belongs to the current running session

The daemon SHALL treat bridge state as authoritative only for a running Claude session whose current identity and process generation match the report and whose last event is less than 45 seconds old. Disabled, absent, stale, or superseded reports SHALL fall back to scraping with a logged source transition. The read API SHALL expose the same liveness decision used by consumers.

#### Scenario: Restart with the same transcript

- **WHEN** a task restarts or recycles, including a restart reusing its transcript id
- **THEN** its previous reports are not live until the new mod reports

#### Scenario: Missing heartbeat

- **WHEN** no event has arrived for 45 seconds
- **THEN** authority returns to the scraper automatically

#### Scenario: Daemon restarts during a busy turn

- **WHEN** a heartbeat repopulates an empty daemon hub
- **THEN** its busy, dialog kind and confidence snapshot reconstructs current state rather than assuming idle

#### Scenario: Concurrent hooks and late posts

- **WHEN** hook event creation overlaps or a retired mod boot posts late
- **THEN** event creation preserves invocation order without awaiting network delivery and retired boots cannot regain authority

### Requirement: Measurement freshness is independent of heartbeat freshness

The hub SHALL record measurement time separately and SHALL clear session-scoped context and cost on a session change.

#### Scenario: Heartbeats without measurements

- **WHEN** a bridge continues heartbeating without a fresh measurement
- **THEN** an old rate-limit reading does not become fresh

### Requirement: Idle-only mod delivery

For a live Claude bridge with bridge.delivery enabled, the daemon SHALL offer shared-notifier deliveries through authenticated claim, authorization and acknowledgment endpoints. The mod SHALL poll after turn.complete and on a gentle idle timer, SHALL submit at most one authorized delivery through $.prompt.submit while locally idle with no pending input dialog, and SHALL leave the user's unsent draft untouched. Every submission SHALL carry a trailing delivery-ID marker. A new mod session SHALL reconcile outstanding claims before requesting new deliveries.

#### Scenario: Draft survives delivery

- **WHEN** the mod delivers an authorized notice while an unsent draft exists
- **THEN** a plugin-framed turn starts and the draft remains unchanged

#### Scenario: Resume reconciles an outstanding claim

- **WHEN** a new authoritative launch resumes a session with an expired authorized claim
- **THEN** the transcript marker completes a submitted delivery without retry, or positive absence after the original launch ended permits re-offer before new work

### Requirement: Cached composable Hera band

The mod SHALL render one truncate-end line above the prompt in the form `argus: <n> unread[ from <sender>] • budget <b>% • <role status>[ • workers <w> working, <k> blocked][ • ⚠ bridge offline]`. The sender SHALL be the newest unread message's and SHALL appear only when unread is above zero. Budget SHALL appear only for coordinator bindings, as live context tokens over the configured budget rounded to a whole percent, with `?` when either is unknown. Role status SHALL be the bound role's Hera status, with distinct statuses joined by `/` when the task holds several bindings. The worker roll-up SHALL sum the snapshot's per-coordinator working and blocked worker counts, omit a zero count, and be absent when both are zero or no coordinator binding exists. The line SHALL NOT show context percent, role name, role kind or orchestrator, which the status line and the TUI rail already show.

The line SHALL be dimmed and SHALL use theme keys for every state color. The one raw color is the `argus:` label, `rgb(59,130,246)`, which matches the statusline's label exactly. The unread segment SHALL be bold `warning` when unread is above zero. Budget SHALL be uncolored below 75%, `warning` from 75%, and bold `warning` from 90%. Role status SHALL be `warning` when blocked, `success` when done and `error` when failed. The blocked worker count and the offline flag SHALL be `warning`.

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
- **THEN** the band reads `argus: 2 unread from newest • budget 90% • blocked • workers 2 working, 1 blocked` with the unread segment and budget bold `warning`, and the status and blocked count `warning`

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

