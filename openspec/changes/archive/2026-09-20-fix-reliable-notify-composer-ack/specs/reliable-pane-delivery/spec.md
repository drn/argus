## MODIFIED Requirements

### Requirement: Pre-clear before inject

The system SHALL emit Ctrl+U before injecting into an empty or notice-only composer so stale previously injected input is discarded. For a notice-only composer, the system SHALL re-read the rendered composer after Ctrl+U and SHALL directly write the replacement notice only when that check confirms the composer is empty. If the clear cannot be confirmed, the system SHALL preserve the draft and append a newline plus a fixed annotation stating that the preceding input was left unsubmitted and must not be acted upon before appending the new notice. For stable abandoned non-notice content, the system SHALL preserve the existing text, SHALL skip Ctrl+U, and SHALL append the same annotation before the current notice. Every notice and every standalone CR SHALL remain separate PTY writes. Before the first CR, the system SHALL allow recipient PTY output to acknowledge and settle after consuming newly injected content. After each CR, the system SHALL confirm from a freshly rendered composer state that the submitted draft was consumed or materially changed before recording the delivery as submitted; PTY output activity alone SHALL NOT be treated as acknowledgment. If that acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after bounded attempts are exhausted, it SHALL leave the delivery pending.

#### Scenario: Empty composer is pre-cleared

- **WHEN** the identifiable composer is empty
- **THEN** Ctrl+U is written before the notice text and standalone CR

#### Scenario: Stale notice content is replaced

- **WHEN** the identifiable composer contains only previously injected Argus/Hera notices and Ctrl+U is confirmed to clear it
- **THEN** the notifier writes the current notice after Ctrl+U

#### Scenario: Unconfirmed stale notice clear is preserved

- **WHEN** a notice-only composer remains non-empty after Ctrl+U
- **THEN** the notifier preserves it and appends the annotation and current notice rather than directly concatenating the replacement

#### Scenario: Abandoned human content is annotated

- **WHEN** stable non-notice composer content is classified as abandoned
- **THEN** Ctrl+U is not written, the existing content is preserved, and the appended input warns the agent not to act on the preceding text before presenting the current notice

#### Scenario: Unacknowledged Enter is retried

- **WHEN** a standalone CR leaves the rendered composer unchanged, including when unrelated recipient output continues
- **THEN** the notifier retries only the standalone CR using bounded backoff

#### Scenario: All Enter attempts remain unacknowledged

- **WHEN** every bounded CR attempt leaves the rendered composer unchanged
- **THEN** the delivery remains pending, is not added to submitted-delivery deduplication state, and can be retried by a later reconcile cycle until its deadline
