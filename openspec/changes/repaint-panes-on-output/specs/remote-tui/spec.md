## ADDED Requirements

### Requirement: Remote sessions deliver streamed output to attached writers

A remote session SHALL deliver each decoded output chunk to its attached writers after appending it to the local ring buffer, and SHALL honor the same attach and replay offsets as other session handles, so that a terminal pane in `--remote` mode can repaint when output arrives. A writer that returns an error SHALL be removed, and a writer SHALL NOT be able to stall the stream reader while holding the session lock.

#### Scenario: Attached writer receives streamed output

- **WHEN** a writer is attached at the current output total and the SSE stream emits an output event
- **THEN** the decoded bytes reach the writer after they are readable from the ring buffer

#### Scenario: Attaching at the current total replays nothing

- **WHEN** a writer is attached with an offset equal to the session's total output
- **THEN** the writer receives no replayed bytes and receives only later output

#### Scenario: A failing writer is removed

- **WHEN** an attached writer returns an error
- **THEN** it is removed and the stream continues to other writers and to the ring buffer
