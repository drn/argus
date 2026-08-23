# REST API

## MODIFIED Requirements

### Requirement: Terminal output, input, and resize

The server SHALL return a task's terminal output, preferring the on-disk session log and falling back to the live ring buffer, advertising a resume cursor (`X-Output-Total`) and source (`X-Source`) so a client can resume a stream without gap or overlap. A task with neither a log nor a live session SHALL return 404 for output. Writing input and reading/setting PTY size SHALL require a live session and return 404 otherwise. Resize SHALL reject zero dimensions and dimensions out of range (greater than 1000).

Writing input SHALL accept an optional origin indicator distinguishing human-typed input from system-injected input (see the `agent-execution` capability). The indicator SHALL default to human origin when absent — the endpoint's only behavior before this indicator existed — so every pre-existing caller (including plugin-scoped tokens that never learn about the indicator) is unaffected. Only a recognized system-origin value SHALL classify the write as system-injected; any other value SHALL also default to human origin.

#### Scenario: Output served from the on-disk log with resume cursor

- **WHEN** an output request targets a task that has a session log on disk
- **THEN** the server returns the log tail, sets `X-Source: log`, and sets `X-Output-Total` to the full log size

#### Scenario: Tail bound does not change the resume cursor

- **WHEN** an output request asks for a tail smaller than the full log
- **THEN** the body is the requested tail but `X-Output-Total` still advertises the full file size

#### Scenario: No log and no session

- **WHEN** an output request targets a task with no log file and no live session
- **THEN** the server responds 404 Not Found

#### Scenario: Input or size without a live session

- **WHEN** a write-input, get-size, or resize request targets a task with no live session
- **THEN** the server responds 404 Not Found

#### Scenario: Resize rejects invalid dimensions

- **WHEN** a resize request supplies zero or out-of-range dimensions on a live session
- **THEN** the server responds 400 Bad Request

#### Scenario: Absent origin indicator defaults to human origin

- **WHEN** a write-input request carries no origin indicator
- **THEN** the write is classified as human origin, advancing both the session's work-cycle and user-input timestamps

#### Scenario: System-origin indicator classifies the write as system-injected

- **WHEN** a write-input request carries the recognized system-origin indicator
- **THEN** the write is classified as system origin, advancing only the session's work-cycle timestamp
