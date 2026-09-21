## MODIFIED Requirements

### Requirement: Pre-clear before inject

The system SHALL emit Ctrl+U before injecting into an empty or notice-only composer so stale previously injected input is discarded. For a notice-only composer, the system SHALL re-read the rendered composer after Ctrl+U and SHALL directly write the replacement notice only when that check confirms the composer is empty. If the clear cannot be confirmed, the system SHALL preserve the draft and append a newline plus a fixed annotation stating that the preceding input was left unsubmitted and must not be acted upon before appending the new notice. A draft containing that annotation and a subsequent Argus/Hera notice SHALL be treated as notifier-generated stale content on a later reconcile, so retries do not append repeated annotations. For stable abandoned non-notice content, the system SHALL preserve the existing text, SHALL skip Ctrl+U, and SHALL append the same annotation before the current notice. Every notice and every standalone CR SHALL remain separate PTY writes. Before the first CR, the system SHALL allow recipient PTY output to acknowledge and settle after consuming newly injected content. A composer snapshot SHALL be considered to contain injected notice text when both strings match after Unicode whitespace is removed, so terminal soft wraps do not prevent verification. After each CR, the system SHALL confirm from a freshly rendered composer state that the submitted draft was consumed or materially changed before recording the delivery as submitted. When no submitted composer snapshot is available, including a recognized composer that cannot reflect the injected text, the system SHALL use output advancement as the conservative fallback acknowledgment. If acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after bounded attempts are exhausted, it SHALL leave the delivery pending unless its total Enter-attempt ceiling has been reached.

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

#### Scenario: Wrapped injected composer is verified

- **WHEN** a delivery text visually wraps across composer rows at the recipient terminal width
- **THEN** the notifier recognizes the whitespace-normalized composer draft as its injected text and uses composer-consumption acknowledgment after Enter

#### Scenario: Known composer cannot reflect injected text

- **WHEN** the terminal identifies a composer but no post-injection draft snapshot contains the injected notice
- **THEN** the notifier uses the output-advance fallback after each standalone Enter rather than leaving acknowledgment false without a wait

#### Scenario: Annotated retry is not appended repeatedly

- **WHEN** a prior unconfirmed delivery left the abandoned-draft annotation followed by an Argus or Hera notice in the composer
- **THEN** the next reconcile recognizes it as stale notifier content and does not append another annotation

#### Scenario: Unacknowledged Enter is retried

- **WHEN** a standalone CR leaves the rendered composer unchanged, including when unrelated recipient output continues
- **THEN** the notifier retries only the standalone CR using bounded backoff

#### Scenario: All Enter attempts remain unacknowledged

- **WHEN** every bounded CR attempt leaves the rendered composer unchanged
- **THEN** the delivery remains pending, is not added to submitted-delivery deduplication state, and can be retried by a later reconcile until its total Enter-attempt ceiling or deadline

### Requirement: Deadline backstop

The system SHALL associate a deadline and an independent total Enter-attempt ceiling with each pending delivery. When the deadline elapses without a successful submit, the delivery SHALL be abandoned (equivalent to the caller calling cancel) and the cancel func SHALL become a no-op. The default deadline is 5 minutes if not supplied by the caller. When the total Enter-attempt ceiling is reached without acknowledgment, the reconciler SHALL abandon the delivery, SHALL not add it to submitted-delivery deduplication state, and SHALL emit an error-level diagnostic identifying the task, delivery, and total attempts.

#### Scenario: Delivery abandoned at deadline

- **WHEN** a delivery has been pending past its deadline
- **THEN** the reconciler removes it without writing to the PTY and the cancel func becomes a no-op

#### Scenario: Delivery abandoned at total attempt ceiling

- **WHEN** a delivery remains unconfirmed across enough reconcile cycles to reach its total Enter-attempt ceiling
- **THEN** the reconciler removes it and emits an error-level delivery-abandoned diagnostic

#### Scenario: Delivery submitted before deadline

- **WHEN** a delivery is submitted before its deadline and total Enter-attempt ceiling
- **THEN** the deadline timer and attempt ceiling have no further effect
