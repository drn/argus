# UX Logging

## Purpose

UX logging provides a dedicated debug log for the Argus TUI layer, written to a file (`~/.argus/ux.log`) that is separate from the daemon's logs. It exists to diagnose hard-to-see TUI failures (tasks failing to start, unexpected auto-completion) and, critically, to give the TUI process a safe sink for `slog`/stdlib-`log` output so those writes never reach the user's live terminal and corrupt the rendered screen.

## Requirements

### Requirement: Timestamped log writes

The system SHALL write each log entry as a single line prefixed with a millisecond-precision timestamp and terminated by a newline, formatting the message from a format string and arguments.

#### Scenario: Writing a formatted entry after initialization

- **WHEN** the log has been initialized and a caller logs with a format string and arguments
- **THEN** the resulting file content contains the fully formatted message on its own line, prefixed by a timestamp

#### Scenario: Multiple entries each occupy one line

- **WHEN** two separate log calls are made
- **THEN** the file contains exactly two lines, each carrying its own timestamp prefix

### Requirement: Logging is a no-op when uninitialized

The system SHALL silently discard log writes when the log file has not been opened, and SHALL NOT panic or error.

#### Scenario: Logging before initialization

- **WHEN** a caller logs while the log file is not open
- **THEN** the call returns without panicking and produces no output

### Requirement: Initialization opens the log file and is idempotent

The system SHALL open the log file for appending (creating it if absent) on first initialization, and SHALL treat any subsequent initialization as a no-op that returns no error while leaving the already-open file in place.

#### Scenario: First initialization succeeds

- **WHEN** initialization is requested with a writable path
- **THEN** the log file is opened and subsequent log calls are written to it

#### Scenario: Repeated initialization is a no-op

- **WHEN** initialization is requested a second time after the file is already open
- **THEN** the call returns no error and does not replace the open file

#### Scenario: Existing log content is preserved

- **WHEN** initialization opens a path that already contains data
- **THEN** new entries are appended rather than overwriting existing content

### Requirement: Default log path derivation

The system SHALL derive the default UX log path by appending the log file name to the supplied data directory.

#### Scenario: Deriving a path from a data directory

- **WHEN** a data directory is supplied for path derivation
- **THEN** the returned path is that directory followed by `/ux.log`

### Requirement: Safe writer accessor for logger redirection

The system SHALL expose a non-nil `io.Writer` for the underlying log file so callers (notably the TUI redirecting the default `slog` and stdlib `log` handlers) can route output into the same file. When the log is not initialized, the system SHALL return a discarding writer rather than nil.

#### Scenario: Writer before initialization returns a safe discard sink

- **WHEN** the writer is requested while the log file is not open
- **THEN** a non-nil discarding writer is returned and writing to it succeeds without error

#### Scenario: Writer after initialization targets the log file

- **WHEN** the writer is requested after the log file is open
- **THEN** a non-discarding writer is returned and bytes written through it appear in the log file

#### Scenario: Redirected logger output stays out of the terminal

- **WHEN** the default `slog` and stdlib `log` handlers are pointed at the writer and emit messages
- **THEN** none of that output reaches the process's standard error, and every message is captured in the log file

### Requirement: Closing releases the log file and is idempotent

The system SHALL close and release the open log file, after which logging reverts to its uninitialized no-op behavior, and SHALL tolerate being closed when no file is open.

#### Scenario: Closing an open log

- **WHEN** the log is closed after being initialized
- **THEN** the file is released and later log calls are discarded until re-initialization

#### Scenario: Closing when nothing is open

- **WHEN** close is invoked while no log file is open
- **THEN** the call completes without error or panic
