## ADDED Requirements

### Requirement: Confirm completion from task-list status advance

In the Tasks tab, a status-advance input that would move an `in_review` task to `complete` SHALL open a confirmation instead of immediately persisting the change. The input that opens the confirmation SHALL NOT also confirm it. Confirming SHALL persist `complete` for the originally selected task; cancelling SHALL preserve `in_review`. Other status advances and reversals SHALL keep their existing behavior.

#### Scenario: Held advance key

- **WHEN** repeated `s` input advances an `in_progress` task to `in_review` and then requests `complete`
- **THEN** the task remains `in_review` until a separate confirmation action occurs

#### Scenario: Selection changes while confirmation is open

- **WHEN** the task-list selection changes after the confirmation opens
- **THEN** confirming still targets the task whose completion was requested

#### Scenario: Cancel completion

- **WHEN** the user cancels the confirmation
- **THEN** the task remains `in_review`
