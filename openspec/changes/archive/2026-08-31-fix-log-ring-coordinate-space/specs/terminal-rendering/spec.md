# Terminal Rendering

## ADDED Requirements

### Requirement: History reads anchor on ring content, not on byte-counter arithmetic

The terminal pane SHALL locate the bytes it needs in the session log by matching the ring tail it already holds against the log's own tail, SHALL NOT use its fed-byte counter (or any comparison between the session handle's total and the log's file size) as an absolute log offset, and SHALL only ever record that fed-byte counter in the session handle's own byte space.

The fed-byte counter counts bytes in the **session handle's** byte space. For a daemon-backed
session handle that space is offset from the on-disk session log's byte offsets by an
arbitrary amount, because the daemon replays only its own bounded ring on stream attach
rather than the session from byte zero.

#### Scenario: Ring-wrap catch-up on a daemon-backed session

- **WHEN** the live emulator needs a catch-up range that the ring tail no longer covers, and the session handle's byte counter is offset from the session log's byte offsets
- **THEN** the pane SHALL feed the log bytes that immediately precede and include the ring tail's content, never the bytes at the raw counter offset

#### Scenario: Ring tail cannot be located in the log

- **WHEN** the ring tail's content cannot be found in the searched region of the session log
- **THEN** the pane SHALL fall back to the existing approximate history rebuild rather than feeding an unanchored range

#### Scenario: Full replay when the log already covers the ring

- **WHEN** a full history rebuild reads a log tail that already contains the ring tail's content
- **THEN** the assembled history SHALL be trimmed to end exactly at the ring tail's end, and the recorded fed-total SHALL be the handle's own ring total

#### Scenario: Full replay when the log lags the ring

- **WHEN** a full history rebuild reads a log tail that holds only a leading part of the ring tail
- **THEN** the uncovered ring suffix SHALL be appended only after verifying that the log's own tail matches the ring tail's corresponding prefix

#### Scenario: Full replay when log and ring cannot be reconciled

- **WHEN** a full history rebuild cannot locate any overlap between the log tail and the ring tail
- **THEN** the pane SHALL feed the ring alone rather than splicing two non-contiguous byte ranges together
