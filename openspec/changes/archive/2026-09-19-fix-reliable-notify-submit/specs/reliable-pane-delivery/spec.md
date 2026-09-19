## MODIFIED Requirements

### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY exactly once and submits it using standalone carriage-return attempts as soon as it is safe to do so. Safe is defined as: the session exists and is idle — either raw output has been quiescent for at least the session idle threshold, or meaningful emulated-screen content has remained stable for the content-idle threshold while the agent's working affordance is absent — AND no human is currently focused on that task's pane (per `FocusTracker.IsFocused`). A delivery SHALL be recorded as submitted only after observable post-CR PTY output acknowledges an attempt. The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Immediate submit when already raw-idle and safe

- **WHEN** `ReliableNotify` is called for a task whose session is raw-idle and unfocused and whose first CR attempt produces PTY output
- **THEN** the text and a standalone carriage return are written to the PTY within the current reconcile cycle, the delivery is recorded as submitted, and the returned cancel func becomes a no-op

#### Scenario: Submit after content-idle convergence despite cosmetic output

- **WHEN** a delivery is pending for an unfocused session that remains raw-busy only because of cosmetic redraw bytes, and its meaningful screen content converges to content-idle
- **THEN** the reconciler attempts the delivery without waiting for raw PTY-byte silence and records success only after post-CR output acknowledgment

#### Scenario: Deferred submit while session is meaningfully busy

- **WHEN** `ReliableNotify` is called for a task whose session is producing meaningful output or shows the working affordance
- **THEN** the delivery is recorded as pending, no PTY write occurs immediately, and the reconciler retries on subsequent ticks until the session becomes raw-idle or content-idle

#### Scenario: Deferred submit while human is focused

- **WHEN** `ReliableNotify` is called for a task whose session is idle but the FocusTracker reports a human is focused on that pane
- **THEN** the delivery is recorded as pending and the reconciler does not write to the PTY until focus leaves

#### Scenario: Human leaves pane, pending delivery submits

- **WHEN** a delivery is pending because a human was focused, and then the focus tracker reports the pane is unfocused
- **THEN** the reconciler attempts the delivery on the next tick provided the session is raw-idle or content-idle

#### Scenario: Cancel before submit abandons delivery

- **WHEN** the caller invokes the cancel func returned by `ReliableNotify` before the delivery has been submitted
- **THEN** no PTY write occurs for that delivery on any subsequent tick

### Requirement: Pre-clear before inject

The system SHALL emit a Ctrl+U (line-kill) signal before injecting the delivery text so that any stale partial input in the shell's line buffer is discarded. If the shell's input line is empty, Ctrl+U SHALL be a no-op at the shell level. The submit sequence SHALL use separate PTY writes for Ctrl+U, delivery text without a trailing CR, and every standalone CR. Before the first CR, the system SHALL allow the recipient's PTY output to acknowledge and settle after consuming the text instead of relying solely on a fixed delay. After each CR, the system SHALL require observable PTY output activity before recording the delivery as submitted. If that acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after the bounded attempts are exhausted, it SHALL leave the delivery pending for a later reconcile cycle rather than mark it submitted.

#### Scenario: Clean input line yields normal submission

- **WHEN** the PTY's shell input line is empty and emits post-CR output activity
- **THEN** Ctrl+U has no visible effect, the text is submitted as the sole input, and the delivery is recorded as submitted

#### Scenario: Stale partial input is discarded

- **WHEN** the PTY's shell input line contains partial text at the moment of submit
- **THEN** Ctrl+U clears the partial text before the delivery text and CR are written

#### Scenario: CR delivered as separate write

- **WHEN** the notifier attempts a delivery
- **THEN** the PTY receives ordered standalone writes for Ctrl+U (0x15), the text without a trailing CR, and CR (0x0D), with no CR appended to the text write

#### Scenario: Slow recipient consumes text before Enter

- **WHEN** a recipient does not emit composer output within the former fixed 50 ms interval but does emit it within the bounded text-consumption window
- **THEN** the notifier waits for that activity to settle before sending the first standalone CR

#### Scenario: Unacknowledged Enter is retried

- **WHEN** a standalone CR produces no subsequent PTY output activity
- **THEN** the notifier retries only the standalone CR using bounded backoff

#### Scenario: All Enter attempts remain unacknowledged

- **WHEN** every bounded CR attempt produces no subsequent PTY output activity
- **THEN** the delivery remains pending, is not added to submitted-delivery deduplication state, and can be retried by a later reconcile cycle until its deadline

## ADDED Requirements

### Requirement: Daemon-visible delivery diagnostics

The system SHALL emit reliable-notify delivery diagnostics through the daemon's structured logging path independently of TUI-only UX-log initialization. Diagnostics SHALL identify the task and delivery, SHALL record successful acknowledged submission, and SHALL record write failures, missing acknowledgment, retries, cancellation, and deadline abandonment at an appropriate severity. Routine idle, focus, and absent-session gate skips MAY be debug-level.

#### Scenario: Acknowledged delivery is logged

- **WHEN** post-CR PTY activity acknowledges a delivery
- **THEN** `daemon.log` receives an informational `[notify]` submission record containing its task ID, delivery ID, and attempt count

#### Scenario: Enter acknowledgment is absent

- **WHEN** a CR attempt produces no PTY output within its acknowledgment window
- **THEN** `daemon.log` receives a warning `[notify]` record containing its task ID, delivery ID, and attempt number

#### Scenario: Delivery write fails

- **WHEN** Ctrl+U, text, or CR cannot be written to the recipient session
- **THEN** `daemon.log` receives a warning `[notify]` record identifying the failed phase, task ID, delivery ID, and error
