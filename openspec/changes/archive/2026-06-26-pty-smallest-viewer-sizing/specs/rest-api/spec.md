## MODIFIED Requirements

### Requirement: Terminal output, input, and resize

The server SHALL return a task's terminal output, preferring the on-disk session log and falling back to the live ring buffer, advertising a resume cursor (`X-Output-Total`) and source (`X-Source`) so a client can resume a stream without gap or overlap. A task with neither a log nor a live session SHALL return 404 for output. Writing input and reading/setting PTY size SHALL require a live session and return 404 otherwise. Resize SHALL reject zero dimensions and dimensions out of range (greater than 1000).

A resize request SHALL register or update the requesting connection as an active viewer of the session under a per-connection viewer ID carrying the supplied `(cols, rows)`, rather than setting an absolute PTY size; the live PTY size is the minimum over active viewers. When the client's output stream disconnects (its request context is cancelled), the server SHALL remove that connection's viewer entry, so closing or navigating away from the web app releases its size claim and the PTY can grow back for remaining viewers. The server SHALL also accept an explicit release for a connection's viewer (used when the client tab becomes hidden) that removes the entry without tearing down the output stream.

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

#### Scenario: Resize registers the connection as a viewer
- **WHEN** a valid resize request is made on a live session
- **THEN** the connection is registered as an active viewer at the supplied dimensions and the PTY is sized to the minimum over active viewers

#### Scenario: Stream disconnect removes the viewer
- **WHEN** the client's output stream request context is cancelled (tab closed or navigated away)
- **THEN** the server removes that connection's viewer entry and the effective PTY size is recomputed over the remaining active viewers

#### Scenario: Explicit release on hide
- **WHEN** the client posts a release for its connection's viewer while keeping the stream open
- **THEN** the viewer entry is removed and the connection no longer constrains the PTY size
