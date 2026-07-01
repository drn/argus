# auto-naming (delta)

## MODIFIED Requirements

### Requirement: Replace the auto-generated slug with an LLM-suggested name

The system SHALL request a task name from the LLM for the given prompt and, when a different valid name is returned, persist it as the task's name. The system SHALL NOT run auto-naming for a task that was created with a user-supplied name (auto-naming is disabled at creation when the user named the task), so an explicitly chosen name is never replaced by an LLM suggestion.

#### Scenario: LLM returns a better name

- **WHEN** auto-naming runs for a task whose current name is the original auto-generated slug and the LLM returns a different non-empty name
- **THEN** the task's persisted name is updated to the LLM-returned name

#### Scenario: LLM returns the same name

- **WHEN** the LLM returns a name equal to the original slug
- **THEN** no rename occurs and the task's name is left unchanged

#### Scenario: Task created with a user-supplied name

- **WHEN** a task is created with an explicit user-supplied name (auto-naming disabled at creation)
- **THEN** auto-naming does not run and the task keeps the user-supplied name
