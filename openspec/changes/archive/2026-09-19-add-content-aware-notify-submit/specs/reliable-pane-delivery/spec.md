## MODIFIED Requirements

### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY exactly once and submits it using standalone carriage-return attempts as soon as it is safe to do so. Safety SHALL be decided primarily from the rendered recipient composer: an empty composer or one containing only previously injected Argus/Hera notice text is safe immediately; non-notice content is unsafe while it changes between snapshots and becomes safe after remaining unchanged for the stability window. Session idle and pane focus SHALL be fallback gates only when a supported composer cannot be identified. A delivery SHALL be recorded as submitted only after observable post-CR PTY output acknowledges an attempt. The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Empty composer submits immediately

- **WHEN** the rendered recipient composer is identifiable and empty
- **THEN** the notifier attempts delivery in the current reconcile cycle regardless of raw session idleness or pane focus

#### Scenario: Previously injected notice submits immediately

- **WHEN** the rendered composer contains only one or more previously injected Argus/Hera notices
- **THEN** the notifier treats the content as stale, clears it, and attempts the current delivery immediately

#### Scenario: Changing non-notice content defers

- **WHEN** the rendered composer contains non-notice text that differs from its prior snapshot
- **THEN** the delivery remains pending and no PTY write occurs in that reconcile cycle

#### Scenario: Stable non-notice content recovers

- **WHEN** non-notice composer text remains unchanged for the stability window
- **THEN** the notifier preserves it, appends a do-not-act annotation and the current notice, and attempts submission

#### Scenario: Unknown composer uses conservative fallback

- **WHEN** the terminal renderer cannot identify a supported composer
- **THEN** the notifier attempts delivery only when the session is idle or content-idle and no human is focused

#### Scenario: Cancel before submit abandons delivery

- **WHEN** the caller invokes the cancel func returned by `ReliableNotify` before the delivery has been submitted
- **THEN** no PTY write occurs for that delivery on any subsequent tick

### Requirement: Pre-clear before inject

The system SHALL emit Ctrl+U before injecting into an empty or notice-only composer so stale previously injected input is discarded. For stable abandoned non-notice content, the system SHALL preserve the existing text, SHALL skip Ctrl+U, and SHALL append a newline plus a fixed annotation stating that the preceding input was left unsubmitted and must not be acted upon before appending the new notice. Every notice and every standalone CR SHALL remain separate PTY writes. Before the first CR, the system SHALL allow recipient PTY output to acknowledge and settle after consuming newly injected content. After each CR, the system SHALL require observable PTY output activity before recording the delivery as submitted. If that acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after bounded attempts are exhausted, it SHALL leave the delivery pending.

#### Scenario: Empty composer is pre-cleared

- **WHEN** the identifiable composer is empty
- **THEN** Ctrl+U is written before the notice text and standalone CR

#### Scenario: Stale notice content is replaced

- **WHEN** the identifiable composer contains only previously injected Argus/Hera notices
- **THEN** Ctrl+U clears the stale notices before the current notice is written

#### Scenario: Abandoned human content is annotated

- **WHEN** stable non-notice composer content is classified as abandoned
- **THEN** Ctrl+U is not written, the existing content is preserved, and the appended input warns the agent not to act on the preceding text before presenting the current notice

#### Scenario: Unacknowledged Enter is retried

- **WHEN** a standalone CR produces no subsequent PTY output activity
- **THEN** the notifier retries only the standalone CR using bounded backoff

#### Scenario: All Enter attempts remain unacknowledged

- **WHEN** every bounded CR attempt produces no subsequent PTY output activity
- **THEN** the delivery remains pending, is not added to submitted-delivery deduplication state, and can be retried by a later reconcile cycle until its deadline

### Requirement: Daemon-visible delivery diagnostics

The system SHALL emit reliable-notify delivery diagnostics through the daemon's structured logging path independently of TUI-only UX-log initialization. Diagnostics SHALL identify the task and delivery, SHALL record composer classification without logging composer text, SHALL record successful acknowledged submission, and SHALL record write failures, missing acknowledgment, retries, cancellation, and deadline abandonment at an appropriate severity.

#### Scenario: Content decision is logged without draft text

- **WHEN** the notifier classifies a composer as empty, notice-only, changing, stable-abandoned, or unknown
- **THEN** `daemon.log` receives a `[notify]` decision record containing task and delivery IDs but not the composer contents

#### Scenario: Acknowledged delivery is logged

- **WHEN** post-CR PTY activity acknowledges a delivery
- **THEN** `daemon.log` receives an informational `[notify]` submission record containing its task ID, delivery ID, and attempt count

#### Scenario: Delivery write fails

- **WHEN** Ctrl+U, text, or CR cannot be written to the recipient session
- **THEN** `daemon.log` receives a warning `[notify]` record identifying the failed phase, task ID, delivery ID, and error
