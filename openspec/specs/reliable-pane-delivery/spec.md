# reliable-pane-delivery Specification

## Purpose
Deliver a message into a task's agent pane exactly once and reliably — injecting the text and submitting it only when the session is idle and unfocused, deduplicating by delivery ID, pre-clearing the prompt, serializing per task, and backstopping with a deadline — so an inbound message reaches the agent's prompt without racing live typing or being silently dropped.
## Requirements
### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY and submits it (single carriage return) exactly once, as soon as it is safe to do so. Safe is defined as: the session exists and is idle (output quiescence ≥ 3 s per `session.IsIdle()`) AND no human is currently focused on that task's pane (per `FocusTracker.IsFocused`). The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Immediate submit when already safe

- **WHEN** `ReliableNotify` is called for a task whose session is idle and unfocused
- **THEN** the text and a carriage return are written to the PTY within the current reconcile cycle, and the returned cancel func becomes a no-op

#### Scenario: Deferred submit while session is busy

- **WHEN** `ReliableNotify` is called for a task whose session is producing output (not yet idle)
- **THEN** the delivery is recorded as pending, no PTY write occurs immediately, and the reconciler retries on subsequent ticks until the session becomes idle

#### Scenario: Deferred submit while human is focused

- **WHEN** `ReliableNotify` is called for a task whose session is idle but the FocusTracker reports a human is focused on that pane
- **THEN** the delivery is recorded as pending and the reconciler does not write to the PTY until focus leaves

#### Scenario: Human leaves pane, pending delivery submits

- **WHEN** a delivery is pending because a human was focused, and then the focus tracker reports the pane is unfocused
- **THEN** the reconciler submits the delivery on the next tick (provided the session is also idle)

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

The system SHALL emit a Ctrl+U (line-kill) signal before injecting the delivery text so that any stale partial input in the shell's line buffer is discarded. If the shell's input line is empty, Ctrl+U SHALL be a no-op at the shell level. The submit sequence is three separate PTY writes: (1) Ctrl+U, (2) the delivery text without any trailing CR, (3) a brief pause (~50 ms), then (4) a standalone CR as its own write. The CR MUST be delivered as a separate `WriteInput` call after the text write — not appended to the text — so that the target shell/agent processes it as a distinct keypress (Enter) rather than as part of a paste sequence.

#### Scenario: Clean input line yields normal submission

- **WHEN** the PTY's shell input line is empty at the moment of submit
- **THEN** the Ctrl+U has no visible effect and the text is submitted as the sole input

#### Scenario: Stale partial input is discarded

- **WHEN** the PTY's shell input line contains partial text at the moment of submit
- **THEN** the Ctrl+U clears the partial text before the delivery text and CR are written

#### Scenario: CR delivered as separate write

- **WHEN** the notifier submits a delivery
- **THEN** the PTY receives three ordered writes: Ctrl+U (0x15), then the text without a trailing CR, then a standalone CR (0x0D) as its own write — never glued to the text write

### Requirement: Deadline backstop

The system SHALL associate a deadline with each pending delivery. When the deadline elapses without a successful submit, the delivery SHALL be abandoned (equivalent to the caller calling cancel) and the cancel func SHALL become a no-op. The default deadline is 5 minutes if not supplied by the caller.

#### Scenario: Delivery abandoned at deadline

- **WHEN** a delivery has been pending past its deadline
- **THEN** the reconciler removes it without writing to the PTY and the cancel func becomes a no-op

#### Scenario: Delivery submitted before deadline

- **WHEN** a delivery is submitted before its deadline
- **THEN** the deadline timer has no further effect

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

