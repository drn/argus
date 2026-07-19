# Clipboard Staging

## Purpose

Clipboard staging provides an ephemeral, per-task buffer where an agent can stage text it wants the user to copy, decoupling the moment the agent produces the text from the moment the user performs the OS-clipboard write. This solves the iOS Safari constraint that a clipboard write must happen inside a synchronous user gesture: the agent stages text ahead of time, then the user takes a single tap (PWA) or keypress (TUI ctrl+y) that performs the real copy. Staged entries are intentionally short-lived — one slot per task, last-write-wins, no persistence across daemon restarts, and an automatic time-to-live expiry.

## Requirements

### Requirement: Per-task staging buffer

The staging store SHALL hold at most one text payload per task, keyed by task ID, with last-write-wins semantics. Payloads for different tasks SHALL be isolated from one another. An empty task ID SHALL be rejected silently — staging a payload under an empty task ID SHALL store nothing and SHALL return no error, and reading or clearing an empty task ID SHALL report absence.

#### Scenario: Stage then read

- **WHEN** text is staged for a task and then read back for the same task
- **THEN** the read returns that text and a present flag of true

#### Scenario: Last write wins

- **WHEN** two payloads are staged in sequence for the same task
- **THEN** a subsequent read returns only the most recently staged text

#### Scenario: Tasks are isolated

- **WHEN** different payloads are staged for two distinct tasks
- **THEN** reading each task returns its own payload and not the other's

#### Scenario: Empty task ID rejected silently

- **WHEN** a payload is staged under an empty task ID
- **THEN** no error is returned and a subsequent read of the empty task ID reports absence

### Requirement: Payload size limit

The store SHALL reject any payload larger than 1 MiB and SHALL return a too-large error identifying the offered size and the maximum. A payload of exactly the maximum size SHALL be accepted. A rejected payload SHALL NOT be stored.

#### Scenario: Oversize payload rejected and not stored

- **WHEN** text larger than the maximum size is staged for a task
- **THEN** a too-large error is returned and a subsequent read of that task reports absence

#### Scenario: Maximum size accepted

- **WHEN** text of exactly the maximum size is staged
- **THEN** the stage succeeds with no error

### Requirement: Time-to-live expiry

A staged payload SHALL expire after a bounded time-to-live and SHALL be reported as absent once expired. Expiry SHALL be enforced lazily on read and also by an explicit prune operation. The default time-to-live SHALL be five minutes.

#### Scenario: Read after expiry reports absence

- **WHEN** a payload is staged and the clock advances past its time-to-live
- **THEN** a read for that task returns absent

#### Scenario: Prune removes expired entries

- **WHEN** a payload has expired and a prune is performed
- **THEN** a subsequent read for that task reports absence

### Requirement: Clearing a staged payload

Clearing a task SHALL remove any staged payload for that task. Clearing a task that has no staged payload SHALL be a no-op.

#### Scenario: Clear removes the payload

- **WHEN** a payload is staged for a task and then the task is cleared
- **THEN** a subsequent read for that task reports absence

#### Scenario: Clear with nothing staged is a no-op

- **WHEN** a task with no staged payload is cleared
- **THEN** the operation completes without error or effect

### Requirement: Change notifications to subscribers

The store SHALL support per-task subscriptions that are notified whenever the task's payload is set, cleared, or expired. A set notification SHALL carry the new text; a clear or expiry notification SHALL carry an empty string. Subscribers SHALL NOT be notified of the existing value at subscription time. Clearing a task that had nothing staged SHALL NOT notify subscribers. Unsubscribing SHALL stop further notifications. Subscriptions for one task SHALL NOT receive notifications for another task.

#### Scenario: Set notifies subscribers with new text

- **WHEN** payloads are staged for a subscribed task
- **THEN** the subscriber is notified once per stage with the staged text in order

#### Scenario: Clear notifies with empty string

- **WHEN** a payload is staged for a subscribed task and then cleared
- **THEN** the subscriber is notified with the staged text and then with an empty string

#### Scenario: Clear without prior stage does not notify

- **WHEN** a subscribed task with nothing staged is cleared
- **THEN** the subscriber is not notified

#### Scenario: Expiry notifies subscribers

- **WHEN** a staged payload for a subscribed task expires and is observed via read or prune
- **THEN** the subscriber is notified with an empty string

#### Scenario: Unsubscribe stops delivery

- **WHEN** a subscriber unsubscribes and a new payload is then staged
- **THEN** the unsubscribed callback receives no further notifications

### Requirement: REST staging endpoints are task-scoped and guarded

The REST API SHALL expose per-task endpoints to read, stage, and clear a task's payload, all keyed by the task ID in the request path. Read SHALL return the staged text with status 200 when present and status 204 with no body when nothing is staged. A request for a task ID that does not exist in the database SHALL return status 404 for read, stage, and clear alike. Staging with a malformed JSON body SHALL return status 400. Staging a payload that exceeds the size limit SHALL return status 400.

#### Scenario: Read with nothing staged

- **WHEN** a read is requested for a known task that has nothing staged
- **THEN** the response is status 204 with no body

#### Scenario: Stage then read round-trip

- **WHEN** text is staged for a known task and then read back
- **THEN** the stage returns status 200 and the read returns status 200 with the staged text

#### Scenario: Clear hides the payload

- **WHEN** text is staged for a known task and then the task is cleared
- **THEN** the clear returns status 200 and a subsequent read returns status 204

#### Scenario: Unknown task returns not found

- **WHEN** a read, stage, or clear targets a task ID not present in the database
- **THEN** the response is status 404

#### Scenario: Oversize body rejected

- **WHEN** a stage request body exceeds the payload size limit
- **THEN** the response is status 400

#### Scenario: Malformed body rejected

- **WHEN** a stage request carries a body that is not valid JSON
- **THEN** the response is status 400

### Requirement: Endpoints degrade gracefully without a configured store

When no staging store has been wired into the API server, read SHALL return status 204 with no body, and stage and clear SHALL return status 503.

#### Scenario: Read without a store

- **WHEN** a read is requested and no staging store is configured
- **THEN** the response is status 204

#### Scenario: Stage and clear without a store

- **WHEN** a stage or clear is requested and no staging store is configured
- **THEN** the response is status 503

### Requirement: Streamed clipboard event encoding

The API SHALL encode a clipboard change as a JSON event for the streaming channel. A present payload SHALL be encoded with a text field carrying the payload, including when the text is empty. An absent payload SHALL be encoded as a cleared sentinel rather than as a text field.

#### Scenario: Present payload encodes a text field

- **WHEN** a clipboard event is encoded for a present payload
- **THEN** the encoded JSON contains a text field with the payload value

#### Scenario: Absent payload encodes a cleared sentinel

- **WHEN** a clipboard event is encoded for an absent payload
- **THEN** the encoded JSON is the cleared sentinel and carries no text field

### Requirement: TUI copy of a staged payload

In the TUI, the staged-clipboard copy keybinding SHALL always be intercepted
and SHALL NOT fall through to normal terminal handling, whether or not a
payload is staged. If a payload is staged, the copy action SHALL copy the
cached staged payload to the OS clipboard and flash a confirmation notice,
and SHALL NOT clear the task's staged payload as a side effect of copying —
the staged payload SHALL remain available for a subsequent copy. If no
payload is staged, the action SHALL flash a notice indicating there is
nothing to copy, and SHALL NOT forward the keypress to the underlying
terminal. If the OS clipboard write fails, no confirmation notice SHALL be
shown. The staged payload SHALL be affected only by its existing lifecycle
(time-to-live expiry, replacement by a newer staged value, or the owning
agent session exiting) — never by the copy action itself.

#### Scenario: Nothing staged is intercepted with a notice

- **WHEN** the copy action runs with no staged payload cached
- **THEN** it reports that nothing was copied and a "nothing to copy" notice
  is shown instead of forwarding the keypress to the terminal

#### Scenario: Copy preserves the staged payload

- **WHEN** the copy action runs with a staged payload cached for a task
- **THEN** it reports a successful copy, the cached payload and the on-screen
  hint remain unchanged, and the task's staged payload is left intact

#### Scenario: Copying twice in a row both succeed

- **WHEN** the copy action is run twice in immediate succession with the same
  staged payload and nothing else changes the staged state in between
- **THEN** both runs report a successful copy of the same text, and neither
  run flashes "nothing to copy"

#### Scenario: OS write failure suppresses the notice

- **WHEN** the OS clipboard write returns an error during a copy
- **THEN** no success callback fires and no confirmation notice is shown

### Requirement: TUI staged-payload hint tracking

The TUI SHALL track the currently staged payload for the active task by
polling the staging source and SHALL show a hint affordance only while a
payload is present. The hint SHALL render with a color that visibly
distinguishes it from the surrounding chrome text, so its presence is
noticeable rather than blending in. When the staging source is unavailable
(for example, when the TUI runs without a daemon-backed runner), tracking
SHALL be a no-op and no hint SHALL be shown. When a previously present
payload becomes absent, the tracked payload SHALL be cleared and the hint
hidden.

#### Scenario: No staging source available

- **WHEN** the staging source is not available and the cache is refreshed
- **THEN** the tracked payload stays empty and no hint is shown

#### Scenario: Present payload shows a visibly distinct hint

- **WHEN** the staging source reports a present payload for the active task
  and the cache is refreshed
- **THEN** the tracked payload is updated to that text, the hint is shown, and
  it renders in a color distinct from the surrounding header/border-title text

#### Scenario: Absent payload hides the hint

- **WHEN** a previously present payload becomes absent and the cache is
  refreshed
- **THEN** the tracked payload is cleared and the hint is hidden

