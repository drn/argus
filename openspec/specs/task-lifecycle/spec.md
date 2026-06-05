# Task Lifecycle

## Purpose

A task is a unit of work an LLM agent performs. Each task carries a status that moves through a fixed workflow (pending, in progress, in review, complete) and timestamps that record when it started and ended. This capability defines the status state machine, its string/JSON serialization, and the timestamp and elapsed-time semantics that downstream UI, persistence, and orchestration layers rely on.

## Requirements

### Requirement: Status workflow ordering

The task status SHALL be one of four ordered states: pending, in progress, in review, complete. Advancing the status SHALL move to the next state in order and SHALL NOT advance past complete. Reversing the status SHALL move to the previous state and SHALL NOT regress below pending.

#### Scenario: Advance from an intermediate state

- **WHEN** advancing a task that is pending or in progress
- **THEN** the status moves to the next state in order (pending to in progress, in progress to in review)

#### Scenario: Advance is clamped at complete

- **WHEN** advancing a task that is already complete
- **THEN** the status remains complete

#### Scenario: Reverse from an intermediate state

- **WHEN** reversing a task that is in review or complete
- **THEN** the status moves to the previous state in order (complete to in review)

#### Scenario: Reverse is clamped at pending

- **WHEN** reversing a task that is already pending
- **THEN** the status remains pending

### Requirement: Status string representation

Each known status SHALL have a stable lowercase identifier: `pending`, `in_progress`, `in_review`, `complete`. An unrecognized status value SHALL render as `unknown(N)` where N is the underlying numeric value.

#### Scenario: Known status names

- **WHEN** rendering the string form of each known status
- **THEN** the result is the corresponding identifier (`pending`, `in_progress`, `in_review`, or `complete`)

#### Scenario: Out-of-range status

- **WHEN** rendering the string form of a status value outside the known range
- **THEN** the result is `unknown(N)` using the numeric value

### Requirement: Status parsing

Parsing SHALL convert a known status identifier into its status value. Parsing an unknown or empty identifier SHALL return an error and default to the pending status value.

#### Scenario: Parse a known identifier

- **WHEN** parsing one of `pending`, `in_progress`, `in_review`, or `complete`
- **THEN** the corresponding status value is returned with no error

#### Scenario: Parse an invalid identifier

- **WHEN** parsing an empty string or any unrecognized identifier
- **THEN** an error is returned and the resulting status defaults to pending

### Requirement: Status text and JSON serialization

A status SHALL serialize to its lowercase string identifier as text, and SHALL serialize as a JSON string (not an integer) when embedded in a JSON document. Deserializing a status from its text identifier SHALL recover the original status, and deserializing an invalid identifier SHALL return an error.

#### Scenario: Text round-trip

- **WHEN** a known status is marshaled to text and then unmarshaled back
- **THEN** the recovered status equals the original

#### Scenario: JSON encodes as a string

- **WHEN** a task status is encoded as part of a JSON document
- **THEN** the status appears as its string identifier (for example `"in_review"`), not a number

#### Scenario: Unmarshaling invalid text fails

- **WHEN** unmarshaling a status from an unrecognized text value
- **THEN** an error is returned

### Requirement: Status transition timestamps

Setting a task's status SHALL record lifecycle timestamps automatically. Entering the in-progress state SHALL set the start time only if it has not already been set, preserving any earlier start time. Entering the complete state SHALL set the end time. Setting any other status SHALL leave both timestamps unchanged.

#### Scenario: Entering in progress sets the start time

- **WHEN** a task with no start time is set to in progress
- **THEN** the start time is set and the end time remains unset

#### Scenario: Entering in progress preserves an existing start time

- **WHEN** a task that already has a start time is set to in progress
- **THEN** the original start time is preserved

#### Scenario: Entering complete sets the end time

- **WHEN** a task is set to complete
- **THEN** the end time is set

#### Scenario: Pending leaves timestamps unset

- **WHEN** a task with no timestamps is set to pending
- **THEN** both the start time and the end time remain unset

### Requirement: Elapsed time computation

A task SHALL report elapsed time as zero when it has not started. While running (started but not ended) it SHALL report the time since the start. Once ended it SHALL report the fixed duration between start and end.

#### Scenario: Not started

- **WHEN** a task has no start time
- **THEN** its elapsed time is zero

#### Scenario: Running

- **WHEN** a task has a start time but no end time
- **THEN** its elapsed time is the duration from the start time to now

#### Scenario: Completed

- **WHEN** a task has both a start time and an end time
- **THEN** its elapsed time is the fixed duration between start and end

### Requirement: Human-readable elapsed string

A task SHALL format its elapsed time as a compact, unit-suffixed string: empty when not started, seconds (`s`) under one minute, minutes (`m`) under one hour, hours (`h`) under one day, and days (`d`) beyond that.

#### Scenario: Not started yields empty string

- **WHEN** a task has not started
- **THEN** its elapsed string is empty

#### Scenario: Sub-minute uses seconds

- **WHEN** the elapsed time is less than one minute
- **THEN** the string is the whole number of seconds suffixed with `s`

#### Scenario: Sub-hour uses minutes

- **WHEN** the elapsed time is at least one minute but less than one hour
- **THEN** the string is the whole number of minutes suffixed with `m`

#### Scenario: Multi-day uses days

- **WHEN** the elapsed time is at least one day
- **THEN** the string is the whole number of days suffixed with `d`

### Requirement: Pinned and archived mutual exclusivity

A task SHALL never be both pinned and archived at the same time. Setting pinned to true SHALL clear archived, and setting archived to true SHALL clear pinned. Clearing either flag SHALL leave the other unchanged.

#### Scenario: Pinning clears archived

- **WHEN** a task is set to pinned
- **THEN** its archived flag is cleared

#### Scenario: Archiving clears pinned

- **WHEN** a task is set to archived
- **THEN** its pinned flag is cleared

### Requirement: Session ID generation

The system SHALL generate a session identifier as a UUID version 4 string in canonical hyphenated form, suitable for pinning an agent conversation.

#### Scenario: Generated identifier is a v4 UUID

- **WHEN** a new session identifier is generated
- **THEN** it is a canonical hyphenated UUID with the version 4 and variant bits set
