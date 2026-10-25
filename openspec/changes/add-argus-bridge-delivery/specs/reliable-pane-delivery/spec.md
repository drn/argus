## ADDED Requirements

### Requirement: Bridge delivery route and fenced recovery

When bridge delivery is enabled and the phase 2 authority seam reports a live Claude bridge, ReliableNotify SHALL retain its shared queue, per-task serialization, cancellation and original five-minute deadline while bypassing PTY composer reads and writes. Claims SHALL be fenced by launch and boot identity. After an unacknowledged claim expires, the system SHALL inspect the resolved transcript for its delivery-ID marker, retrying briefly for async flush lag. A found marker SHALL complete delivery without resubmission; a positively absent marker in a readable transcript covering the lease window SHALL permit one mod re-offer then PTY fallback after a bounded timeout. Unreadable or incomplete evidence SHALL abandon automatic submission with uxlog and slog diagnostics, leaving durable mail unread; later evidence checks MAY complete delivery. Retired claims SHALL never authorize submission after fallback. Disabled delivery, disabled bridge, missing/dead bridges and non-Claude backends SHALL retain the existing PTY path.

#### Scenario: Submit succeeded before ack was lost

- **WHEN** a claim expires and its marker appears in the transcript
- **THEN** delivery completes without a second mod or PTY submission

#### Scenario: Live session settles after an unsubmitted claim

- **WHEN** a readable flushed transcript covering the lease window lacks the delivery marker and the same launch/session reports an idle heartbeat more than the flush-lag allowance after lease expiry
- **THEN** the system treats submission as positively absent, re-offers once and subsequently permits bounded PTY fallback
