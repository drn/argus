## MODIFIED Requirements

### Requirement: Pre-clear before inject

The system SHALL emit Ctrl+U before injecting into an empty or notice-only composer so stale previously injected input is discarded. For a notice-only composer, the system SHALL re-read the rendered composer after Ctrl+U and SHALL directly write the replacement notice only when that check confirms the composer is empty. If the clear cannot be confirmed, the system SHALL preserve the draft and append a newline plus a fixed annotation stating that the preceding input was left unsubmitted and must not be acted upon before appending the new notice. A draft containing that annotation and a subsequent Argus/Hera notice SHALL be treated as notifier-generated stale content on a later reconcile, so retries do not append repeated annotations. For stable abandoned non-notice content, the system SHALL capture the existing non-placeholder text, SHALL clear it with Ctrl+U and confirm the composer is empty, SHALL submit only the current notice, and SHALL restore the captured text as an unsent draft after the notice is acknowledged. If clear confirmation for the same captured stable draft remains absent for the bounded per-delivery clear-attempt limit, the system SHALL preserve that captured text through the same fixed annotation and submit the current notice rather than leaving the delivery pending until its deadline. The restore SHALL be a text-only write with no carriage return and SHALL occur only after a fresh recognizable composer snapshot confirms it is empty; otherwise the system SHALL skip restoration and emit a warning diagnostic without logging composer text. Faint-only placeholder content SHALL be treated identically to an empty composer and SHALL never be captured or restored. Every notice and every standalone CR SHALL remain separate PTY writes. Before the first CR, the system SHALL allow recipient PTY output to acknowledge and settle after consuming newly injected content. A composer snapshot SHALL be considered to contain injected notice text when both strings match after Unicode whitespace is removed, so terminal soft wraps do not prevent verification. After each CR, the system SHALL confirm from a freshly rendered composer state that the submitted draft was consumed or materially changed before recording the delivery as submitted. When no submitted composer snapshot is available, including a recognized composer that cannot reflect the injected text, the system SHALL use output advancement as the conservative fallback acknowledgment. If acknowledgment is absent, the system SHALL retry only the standalone CR with bounded backoff; after bounded attempts are exhausted, it SHALL leave the delivery pending unless its total Enter-attempt ceiling has been reached.

#### Scenario: Empty composer is pre-cleared

- **WHEN** the identifiable composer is empty
- **THEN** Ctrl+U is written before the notice text and standalone CR

#### Scenario: Stale notice content is replaced

- **WHEN** the identifiable composer contains only previously injected Argus/Hera notices and Ctrl+U is confirmed to clear it
- **THEN** the notifier writes the current notice after Ctrl+U

#### Scenario: Unconfirmed stale notice clear is preserved

- **WHEN** a notice-only composer remains non-empty after Ctrl+U
- **THEN** the notifier preserves it and appends the annotation and current notice rather than directly concatenating the replacement

#### Scenario: Stable abandoned human content is restored cleanly

- **WHEN** stable non-notice composer content is classified as abandoned and Ctrl+U is confirmed
- **THEN** the notifier submits only the current notice and writes the captured draft back without a carriage return after acknowledgment

#### Scenario: Stable draft clear falls back after bounded retries

- **WHEN** the same captured stable non-notice composer content remains non-empty after Ctrl+U across the clear-attempt limit
- **THEN** the notifier submits the captured content plus the fixed annotation and current notice without waiting for the delivery deadline

#### Scenario: Faint placeholder is not restored

- **WHEN** the identifiable composer contains only faint-styled placeholder content
- **THEN** the notifier follows the empty-composer path and never enters the capture, clear, and restore cycle

#### Scenario: Changed composer skips restoration

- **WHEN** a stable draft was captured but the recognizable composer is non-empty or unrecognizable after notice acknowledgment
- **THEN** the notifier does not write the captured draft and emits a warning diagnostic that restoration was skipped

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
