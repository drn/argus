# Daemon Client

## MODIFIED Requirements

### Requirement: Forwarding terminal input

A session handle SHALL forward written input to the daemon for delivery to the PTY, reporting the number of bytes accepted, and SHALL track the wall-clock time of the most recent input. Input written after a session is closed SHALL not block indefinitely. Consecutive inputs MAY be coalesced into fewer RPCs, but coalescing SHALL stop at a bracketed-paste end boundary so two back-to-back paste cycles are never merged into a single write.

Every forwarded input SHALL carry an explicit origin (human or system-injected — see the `agent-execution` capability's input-forwarding requirement) across the RPC as a request field. Coalescing SHALL ALSO stop at an origin boundary: two consecutive queued writes with different origins SHALL NOT be merged into a single RPC, since origin is a per-request attribute and merging would misattribute one write's origin to the other's bytes. An item that cannot be merged for this reason SHALL be carried forward to the next RPC rather than dropped or silently absorbed.

The origin field SHALL be safely additive on the wire: its absence (an older peer's request) SHALL be interpreted identically to the human origin, which is the only behavior any pre-existing peer ever exhibited, so a version-mismatched daemon/supervisor pair SHALL continue to function without error — a system-origin write simply degrades to human-origin semantics on a hop where the far side predates this field, rather than being rejected or corrupting the connection.

#### Scenario: Write reports byte count and advances last-input time

- **WHEN** input is written to a live session handle
- **THEN** the call SHALL report the number of bytes written and the handle's last-input time SHALL advance from its zero value

#### Scenario: Coalescing plain input

- **WHEN** multiple plain (non-paste) inputs of the SAME origin are pending
- **THEN** they SHALL be combined into a single buffer with nothing left pending

#### Scenario: Flush at paste boundary

- **WHEN** a buffer already ends with a bracketed-paste end sequence and more input is pending
- **THEN** coalescing SHALL stop and the still-pending input SHALL remain for a later flush

#### Scenario: Two back-to-back pastes stay separate

- **WHEN** two complete bracketed-paste cycles are queued back to back
- **THEN** each cycle SHALL be drained as its own buffer rather than merged into one

#### Scenario: Origin boundary stops coalescing

- **WHEN** a queued input has a different origin than the batch currently being assembled
- **THEN** coalescing SHALL stop before that item, it SHALL be carried forward rather than dropped, and it SHALL be sent as its own subsequent RPC

#### Scenario: A version-mismatched peer defaults the origin to human, not an error

- **WHEN** an input-forwarding request arrives without an origin field (an older peer)
- **THEN** the receiving side SHALL treat it as human-origin input and process it exactly as it would have before this field existed
