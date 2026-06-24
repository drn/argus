# Task Auto-Naming

## MODIFIED Requirements

### Requirement: Fail open on any LLM error

The system SHALL keep the original auto-generated slug whenever the LLM call
fails, and SHALL NOT propagate the error to the caller. When the LLM CLI exits
non-zero, the system SHALL include the CLI's emitted failure reason in the
logged error regardless of whether the CLI wrote it to stdout or stderr, so the
cause (e.g. budget exceeded, usage/rate limit, overload) is diagnosable rather
than a bare exit-status code.

#### Scenario: LLM call returns an error

- **WHEN** the LLM name request returns an error
- **THEN** the task's name remains the original slug and the operation completes without surfacing the error

#### Scenario: LLM backend unavailable

- **WHEN** the LLM backend is unavailable
- **THEN** auto-naming is skipped and the task's name remains the original slug

#### Scenario: Prompt is empty

- **WHEN** the prompt passed to auto-naming is empty
- **THEN** auto-naming is skipped and the task's name remains unchanged

#### Scenario: CLI writes its failure reason to stdout

- **WHEN** the LLM CLI exits non-zero and wrote its failure reason to stdout (with stderr empty)
- **THEN** the wrapped error includes that stdout reason instead of only a bare exit-status code

## ADDED Requirements

### Requirement: Retry once on a transient LLM failure

The system SHALL retry the LLM name request a single time, after a short
backoff, when the CLI exits non-zero — so a momentary overload, rate limit, or
budget blip does not permanently strand the original slug. The retry SHALL NOT
apply to the skip cases (CLI unavailable, empty prompt) or to output that ran
successfully but failed validation; those return immediately as before. If the
retry also fails, the system SHALL fail open with the (now diagnosable) error.

#### Scenario: First call fails, retry succeeds

- **WHEN** the first LLM CLI invocation exits non-zero and a retry returns a valid name
- **THEN** the task is renamed to the retried name

#### Scenario: Both attempts fail

- **WHEN** both the initial invocation and its single retry exit non-zero
- **THEN** auto-naming fails open, the slug is kept, and the logged error carries the CLI's failure reason

#### Scenario: Skip cases are not retried

- **WHEN** the CLI is unavailable or the prompt is empty
- **THEN** the request returns the skip result immediately without a retry

### Requirement: Budget cap sized above measured per-call cost

The system SHALL cap each LLM name request with a per-call USD budget set
comfortably above the measured per-call cost, so a normal prompt — including a
moderately long pasted one — does not exceed the cap and fail. The cap SHALL be
documented against the measured token usage, not a stale estimate.

#### Scenario: Normal call stays within budget

- **WHEN** auto-naming runs for a typical task prompt
- **THEN** the per-call budget passed to the CLI exceeds the measured per-call cost with headroom, so the call is not terminated for exceeding budget
