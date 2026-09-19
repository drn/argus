## MODIFIED Requirements

### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY and submits it (single carriage return) exactly once, as soon as it is safe to do so. Safe is defined as: the session exists and is idle — either raw output has been quiescent for at least the session idle threshold, or meaningful emulated-screen content has remained stable for the content-idle threshold while the agent's working affordance is absent — AND no human is currently focused on that task's pane (per `FocusTracker.IsFocused`). The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Immediate submit when already raw-idle and safe

- **WHEN** `ReliableNotify` is called for a task whose session is raw-idle and unfocused
- **THEN** the text and a carriage return are written to the PTY within the current reconcile cycle, and the returned cancel func becomes a no-op

#### Scenario: Submit after content-idle convergence despite cosmetic output

- **WHEN** a delivery is pending for an unfocused session that remains raw-busy only because of cosmetic redraw bytes, and its meaningful screen content converges to content-idle
- **THEN** the reconciler submits the delivery without waiting for raw PTY-byte silence

#### Scenario: Deferred submit while session is meaningfully busy

- **WHEN** `ReliableNotify` is called for a task whose session is producing meaningful output or shows the working affordance
- **THEN** the delivery is recorded as pending, no PTY write occurs immediately, and the reconciler retries on subsequent ticks until the session becomes raw-idle or content-idle

#### Scenario: Deferred submit while human is focused

- **WHEN** `ReliableNotify` is called for a task whose session is idle but the FocusTracker reports a human is focused on that pane
- **THEN** the delivery is recorded as pending and the reconciler does not write to the PTY until focus leaves

#### Scenario: Human leaves pane, pending delivery submits

- **WHEN** a delivery is pending because a human was focused, and then the focus tracker reports the pane is unfocused
- **THEN** the reconciler submits the delivery on the next tick provided the session is raw-idle or content-idle

#### Scenario: Cancel before submit abandons delivery

- **WHEN** the caller invokes the cancel func returned by `ReliableNotify` before the delivery has been submitted
- **THEN** no PTY write occurs for that delivery on any subsequent tick
