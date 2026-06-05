# Task Auto-Naming

## Purpose

Newly created tasks start with a regex-derived slug taken from the prompt, which is often terse or awkward. Auto-naming asks an LLM (Haiku) for a clearer, human-readable name and applies it to the task in the background. The process is fire-and-forget and fail-open: any failure leaves the original slug untouched, and it never clobbers a name the user changed in the meantime.

## Requirements

### Requirement: Replace the auto-generated slug with an LLM-suggested name

The system SHALL request a task name from the LLM for the given prompt and, when a different valid name is returned, persist it as the task's name.

#### Scenario: LLM returns a better name

- **WHEN** auto-naming runs for a task whose current name is the original auto-generated slug and the LLM returns a different non-empty name
- **THEN** the task's persisted name is updated to the LLM-returned name

#### Scenario: LLM returns the same name

- **WHEN** the LLM returns a name equal to the original slug
- **THEN** no rename occurs and the task's name is left unchanged

### Requirement: Fail open on any LLM error

The system SHALL keep the original auto-generated slug whenever the LLM call fails, and SHALL NOT propagate the error to the caller.

#### Scenario: LLM call returns an error

- **WHEN** the LLM name request returns an error
- **THEN** the task's name remains the original slug and the operation completes without surfacing the error

#### Scenario: LLM backend unavailable

- **WHEN** the LLM backend is unavailable
- **THEN** auto-naming is skipped and the task's name remains the original slug

#### Scenario: Prompt is empty

- **WHEN** the prompt passed to auto-naming is empty
- **THEN** auto-naming is skipped and the task's name remains unchanged

### Requirement: Never overwrite an externally changed name

The system SHALL apply the LLM-suggested name only if the task's current name still equals the original slug it started from, so a name changed externally while the LLM call was in flight is preserved.

#### Scenario: User renames during the LLM call

- **WHEN** the task is renamed by the user before the LLM returns, so the current name no longer equals the original slug
- **THEN** the LLM-suggested name is not applied and the user's chosen name is preserved

### Requirement: Tolerate a deleted task

The system SHALL handle the case where the task no longer exists when the LLM result is ready, completing without error and without writing any name.

#### Scenario: Task deleted before the LLM returns

- **WHEN** the task has been deleted before the LLM-suggested name is ready to apply
- **THEN** the operation completes without panic and no name is written

### Requirement: Bound the LLM call with a timeout

The system SHALL invoke the LLM name request under a context with a deadline so the operation cannot run unbounded.

#### Scenario: LLM call receives a deadline-bound context

- **WHEN** auto-naming invokes the LLM name request
- **THEN** the request is given a context that carries a deadline and the overall operation does not exceed the configured timeout
