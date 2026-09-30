# reliable-pane-delivery Specification

## Purpose
Deliver a message into a task's agent pane exactly once and reliably — injecting the text and submitting it only when the session is idle and unfocused, deduplicating by delivery ID, pre-clearing the prompt, serializing per task, and backstopping with a deadline — so an inbound message reaches the agent's prompt without racing live typing or being silently dropped.
## Requirements
### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY exactly once and submits it using standalone carriage-return attempts as soon as it is safe to do so. Safety SHALL be decided primarily from the rendered recipient composer: an empty composer, including one whose only visible draft text is dim-styled Claude Code placeholder content, or one containing only previously injected Argus/Hera notice text is safe immediately; non-notice content is unsafe while it changes between snapshots and becomes safe after remaining unchanged for the stability window. Session idle and pane focus SHALL be fallback gates only when a supported composer cannot be identified. A delivery SHALL be recorded as submitted only after a freshly rendered composer confirms the submitted draft was consumed or materially changed; PTY output activity alone SHALL NOT acknowledge an attempt. The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Empty composer submits immediately

- **WHEN** the rendered recipient composer is identifiable and empty
- **THEN** the notifier attempts delivery in the current reconcile cycle regardless of raw session idleness or pane focus

#### Scenario: Dim placeholder composer submits immediately

- **WHEN** the identifiable composer contains only dim-styled Claude Code placeholder text
- **THEN** the notifier treats it as empty and does not preserve or annotate the placeholder

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

### Requirement: Exactly-once deduplication by deliveryID

The system SHALL ensure that re-posting the same deliveryID for the same task is a no-op after that delivery has been submitted. If the deliveryID is currently pending, the re-post SHALL return the existing cancel func without registering a second delivery. If the deliveryID has already been submitted, the re-post SHALL return a no-op cancel func immediately.

#### Scenario: Re-post of pending deliveryID returns same cancel

- **WHEN** `ReliableNotify` is called twice with the same taskID and deliveryID while the first delivery is still pending
- **THEN** no duplicate delivery is registered and the second call returns the same logical cancel

#### Scenario: Re-post of submitted deliveryID is a no-op

- **WHEN** `ReliableNotify` is called with a deliveryID that has already been submitted
- **THEN** the call returns immediately with a no-op cancel and no second PTY write is scheduled

### Requirement: Pre-clear before inject

The system SHALL emit Ctrl+U before injecting into an empty or notice-only composer so stale previously injected input is discarded. For a notice-only composer, the system SHALL re-read the rendered composer after Ctrl+U and SHALL directly write the replacement notice only when that check confirms the composer is empty. If the clear cannot be confirmed, the system SHALL preserve the draft and append the current bracketed Argus/Hera notice followed by a fixed, directionless annotation that identifies unsubmitted user input in the composer, directs the agent not to act on it, directs the agent to handle only the bracketed notice, and asks the agent to ask the user to continue their thought afterward. A draft containing an Argus/Hera notice before that annotation SHALL be treated as notifier-generated stale content on a later reconcile, so retries do not append repeated annotations. For stable abandoned non-notice content, the system SHALL capture the existing non-placeholder text, SHALL clear it with Ctrl+U and confirm the composer is empty, SHALL submit only the current notice, and SHALL restore the captured text as an unsent draft after the notice is acknowledged. The system SHALL remember, per task, the exact content it last restored this way, and SHALL treat that same content observed as a subsequent stable draft for the same task as already-notifier-restored rather than a newly abandoned human draft: it SHALL clear and submit the notice without repeating capture and restore. This check SHALL be one-shot, consumed on the next stable-draft observation for that task regardless of whether it matches, so a later human draft that happens to repeat the same words is still captured and restored normally. If clear confirmation for the same captured stable draft remains absent for the bounded per-delivery clear-attempt limit, the system SHALL preserve that captured text through the same fixed annotation and submit the current notice rather than leaving the delivery pending until its deadline. After the first unconfirmed clear for a captured stable draft, the system SHALL treat that delivery as suspect: every later apparent clear SHALL use the fixed annotation payload and SHALL discard the captured restoration text rather than restoring it. The restore SHALL be a text-only write with no carriage return and SHALL occur only after a fresh recognizable composer snapshot confirms it is empty; otherwise the system SHALL skip restoration and emit a warning diagnostic without logging composer text. Faint-only placeholder content SHALL be treated identically to an empty composer and SHALL never be captured or restored. Every notice and every standalone CR SHALL remain separate PTY writes. Before the first CR, the system SHALL allow recipient PTY output to acknowledge and settle after consuming newly injected content. A composer snapshot SHALL be considered to contain injected notice text when both strings match after Unicode whitespace is removed, so terminal soft wraps do not prevent verification. After each CR, the system SHALL confirm from a freshly rendered composer state that the submitted draft was consumed or materially changed before recording the delivery as submitted. When no submitted composer snapshot is available, including a recognized composer that cannot reflect the injected text, the system SHALL use output advancement as the conservative fallback acknowledgment. If acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after bounded attempts are exhausted, it SHALL leave the delivery pending unless its total Enter-attempt ceiling has been reached.

#### Scenario: Unconfirmed stale notice clear is preserved safely

- **WHEN** a notice-only composer remains non-empty after Ctrl+U
- **THEN** the notifier preserves it, writes the current bracketed notice followed by the directionless abandoned-input annotation, and does not directly concatenate the replacement

#### Scenario: Reordered annotated retry is not appended repeatedly

- **WHEN** a prior unconfirmed delivery left an Argus or Hera notice before the abandoned-draft annotation in the composer
- **THEN** the next reconcile recognizes it as stale notifier content and does not append another annotation

### Requirement: Deadline backstop

The system SHALL associate a deadline and an independent total Enter-attempt ceiling with each pending delivery. When the deadline elapses without a successful submit, the delivery SHALL be abandoned (equivalent to the caller calling cancel) and the cancel func SHALL become a no-op. The default deadline is 5 minutes if not supplied by the caller. When the total Enter-attempt ceiling is reached without acknowledgment, the reconciler SHALL abandon the delivery, SHALL not add it to submitted-delivery deduplication state, and SHALL emit an error-level diagnostic identifying the task, delivery, and total attempts.

#### Scenario: Delivery abandoned at deadline

- **WHEN** a delivery has been pending past its deadline
- **THEN** the reconciler removes it without writing to the PTY and the cancel func becomes a no-op

#### Scenario: Delivery abandoned at total attempt ceiling

- **WHEN** a delivery remains unconfirmed across enough reconcile cycles to reach its total Enter-attempt ceiling
- **THEN** the reconciler removes it and emits an error-level delivery-abandoned diagnostic

#### Scenario: Delivery submitted before deadline

- **WHEN** a delivery is submitted before its deadline and total Enter-attempt ceiling
- **THEN** the deadline timer and attempt ceiling have no further effect

### Requirement: Per-task submit serialization

The system SHALL serialize all auto-submit PTY writes for a given task such that no two concurrent `ReliableNotify` callers can write to the same task's PTY simultaneously. At most one delivery per task SHALL be in the "submitting" state at any moment.

#### Scenario: Concurrent deliveries for the same task are serialized

- **WHEN** two callers each register a delivery for the same task
- **THEN** the second delivery does not start its PTY write until the first has completed

### Requirement: Reconciler driven by idle-watcher tick

The system SHALL expose a `Reconcile(now time.Time)` method that processes all pending deliveries. The method SHALL be called by the daemon's idle-watcher tick (5-second interval) and SHALL call `session.IsIdle()` and `FocusTracker.IsFocused` directly rather than relying on event consumption.

#### Scenario: Reconcile processes all pending deliveries

- **WHEN** `Reconcile` is called while one delivery is pending and its session is idle and unfocused
- **THEN** that delivery is submitted during the same `Reconcile` call

#### Scenario: Reconcile skips deliveries for sessions not yet idle

- **WHEN** `Reconcile` is called and a pending delivery's session is not idle
- **THEN** the delivery remains pending and no PTY write occurs

### Requirement: No PTY write when session is absent

The system SHALL skip pending deliveries for tasks whose session is not currently live (runner returns nil). The delivery SHALL remain pending and be retried on the next tick.

#### Scenario: Missing session defers delivery

- **WHEN** `Reconcile` is called for a pending delivery whose task has no live session
- **THEN** no PTY write occurs and the delivery remains pending until the next tick or its deadline

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

