## ADDED Requirements

### Requirement: Visible terminal panes repaint when output arrives

The TUI SHALL repaint when output arrives for a session shown in a terminal pane that was drawn in the most recent frame, covering the Tasks agent view pane and both Hera panes. It SHALL NOT rely on a timer to paint that output. Repaints SHALL be coalesced under a minimum frame interval of about 8ms, and at most one such repaint SHALL be queued at a time. The first output after a quiet period SHALL queue a repaint immediately. The output notification SHALL NOT block the session's stream reader. This mechanism SHALL NOT call `screen.Sync()`.

#### Scenario: Echo paints without waiting for a timer

- **WHEN** a pane on screen is quiet and its session produces output
- **THEN** a repaint is queued immediately, without waiting for the spinner loop or the tick

#### Scenario: A Hera pane repaints on output

- **WHEN** a session bound to a visible Hera coordinator or agent pane produces output
- **THEN** that pane repaints the same way as the Tasks agent view

#### Scenario: A burst of output is coalesced

- **WHEN** a session produces many chunks within one frame interval
- **THEN** no more than one repaint starts in that interval, and output arriving during a repaint causes one further repaint

#### Scenario: Output for a session not on screen causes no repaint

- **WHEN** a session produces output and no pane drawn in the most recent frame shows it
- **THEN** that output does not queue a repaint

#### Scenario: Only the drawn pane counts as visible

- **WHEN** a Hera pane is fullscreen and the other pane is not drawn
- **THEN** output for the session in the undrawn pane does not queue a repaint

#### Scenario: A quiet pane causes no repaints

- **WHEN** a pane on screen shows a session that produces no output
- **THEN** this mechanism queues no repaints

#### Scenario: A pane bound to a new session is picked up

- **WHEN** a visible pane is rebound to a different session
- **THEN** after the next frame, output from the new session repaints and output from the old session does not

#### Scenario: A session ending repaints its pane

- **WHEN** a session shown in a visible pane ends
- **THEN** one repaint is queued so the finished state appears without waiting for the tick

### Requirement: Terminal size queries are bounded without delaying resize notifications

The TUI's terminfo TTY SHALL cache successful terminal size queries for at most 1 second, retaining normal screen clearing, drawing and flushing. It SHALL invalidate the cache before forwarding a resize notification and on Start, resume and Stop. Failed size queries SHALL pass through unchanged and SHALL NOT be cached. Cache access and invalidation SHALL be safe across concurrent drawing and resize notifications. The cache SHALL NOT change daemon or supervisor protocols.

#### Scenario: Repeated frames reuse a size query

- **WHEN** multiple frames flush within 1 second without a resize notification or lifecycle change
- **THEN** they reuse one successful terminal size query

#### Scenario: Resize invalidates before its callback

- **WHEN** the TTY delivers a resize notification
- **THEN** a size query from the forwarded callback observes a fresh underlying query

#### Scenario: A missing notification recovers within the TTL

- **WHEN** terminal dimensions change without a resize notification
- **THEN** a query at or after 1 second fetches fresh dimensions

#### Scenario: Lifecycle changes discard cached dimensions

- **WHEN** the TTY starts, resumes through Start, or stops
- **THEN** subsequent size queries cannot reuse dimensions cached before that operation

#### Scenario: A failed query is retried

- **WHEN** the underlying size query returns an error
- **THEN** that result is returned unchanged and the next query reaches the underlying TTY again

### Requirement: Keystroke-to-paint latency is logged

The timing log SHALL be off by default and SHALL be enabled only for a TUI launched with `ARGUS_TYPING_LOG=1`. When it is off, the TUI SHALL NOT take latency timestamps or write latency log lines. When it is on, the TUI SHALL write one `uxlog` line prefixed `[typing]` for each key forwarded to an agent from the Tasks agent view or a Hera pane, reporting the total time from the key press to the frame containing its echo being flushed, and the parts of that time: waiting to be handled, waiting for output, waiting for paint, and time spent inside a tick closure. The first output after the key SHALL be treated as its echo. The line SHALL also report the time since the session's last prior output and whether another key was still awaiting its echo. The line SHALL NOT contain key content or length.

#### Scenario: The log is off by default

- **WHEN** the TUI is launched without `ARGUS_TYPING_LOG=1` and keys are forwarded to an agent
- **THEN** no `[typing]`, `[tick]` or `[repaint]` line is written and no latency timestamps are taken

#### Scenario: An echoed key logs its latency

- **WHEN** the timing log is on, a key is forwarded to a live session, and output follows and is painted
- **THEN** one `[typing]` line is logged with the task, the pane, the total time and each part

#### Scenario: Time behind a stalled UI goroutine is counted

- **WHEN** the timing log is on and a key is pressed while a tick closure is running on the UI goroutine
- **THEN** the wait is included in the total, in the time waiting to be handled, and in the time spent inside a tick closure

#### Scenario: A key with no echo is not paired with later output

- **WHEN** the timing log is on and a forwarded key produces no output within 2 seconds
- **THEN** a `[typing]` line is logged with `outcome=no-echo`, and later output is not attributed to it

#### Scenario: Overlapping keys are marked

- **WHEN** the timing log is on and a key is forwarded while an earlier key is still waiting for its echo
- **THEN** its line is marked `overlap=1`

#### Scenario: Key content is never logged

- **WHEN** any `[typing]` line is written
- **THEN** it contains only identifiers and timings, and no key value or input length

### Requirement: Slow tick closures are logged

While the timing log is on, the TUI SHALL write a `uxlog` line prefixed `[tick]` with the duration of any tick closure on the UI goroutine that takes 16ms or longer, and SHALL NOT log faster ticks.

#### Scenario: A slow tick is logged

- **WHEN** the timing log is on and a tick closure takes 16ms or longer
- **THEN** a `[tick]` line with its duration is logged

#### Scenario: A fast tick is not logged

- **WHEN** the timing log is on and a tick closure takes less than 16ms
- **THEN** no `[tick]` line is logged
