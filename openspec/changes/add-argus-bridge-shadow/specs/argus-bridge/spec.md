## ADDED Requirements

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
