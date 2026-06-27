# forms-and-modals (delta)

## ADDED Requirements

### Requirement: Optional task-name field

The new-task form SHALL render an OPTIONAL single-line name input positioned immediately after the prompt field, with placeholder text `(optional)`. The field SHALL participate in the form's Tab/Backtab focus order in that position, SHALL accept pasted text via the form's paste handler, and SHALL treat a whitespace-only value as blank (equivalent to no name supplied).

#### Scenario: Name field renders after the prompt with placeholder

- **WHEN** the new-task form is displayed and the name field is empty and unfocused
- **THEN** a single-line name input is shown immediately after the prompt field with the placeholder `(optional)`

#### Scenario: Name field is reachable in focus order

- **WHEN** the user advances focus with Tab from the prompt field
- **THEN** the name field receives focus before the focus order wraps

#### Scenario: Name field accepts pasted text

- **WHEN** the name field is focused and the user pastes text
- **THEN** the pasted text is inserted into the name field

#### Scenario: Whitespace-only name is treated as blank

- **WHEN** the user submits with a name field containing only whitespace
- **THEN** the form behaves as if no name was supplied (the name is derived from the prompt)

## MODIFIED Requirements

### Requirement: New-task form submission

The new-task form SHALL produce a task only when a known project is selected and the prompt is non-empty, and SHALL surface an error and remain open otherwise. The submitted task SHALL carry the resolved project, the resolved branch, the selected backend, the trimmed prompt, a name, and pending status. The name SHALL be the trimmed, sanitized value of the optional name field when that field is non-empty; otherwise it SHALL be an auto-generated name derived from the prompt. When the user supplied an explicit (non-blank) name, the form SHALL signal that the name is user-chosen so the creation path suppresses background auto-naming (it does not run the LLM rename); when no name was supplied, background auto-naming is unaffected.

#### Scenario: Submit with unknown project

- **WHEN** the user submits the prompt field while the project text matches no known project
- **THEN** the form sets an "Unknown project" error and does not mark itself done

#### Scenario: Submit with empty prompt

- **WHEN** the project resolves to a known project but the prompt is empty
- **THEN** the form does not mark itself done

#### Scenario: Successful submission

- **WHEN** the project resolves and the prompt is non-empty and the user submits
- **THEN** the form marks itself done and yields a pending task with the resolved project, resolved branch, selected backend, and trimmed prompt

#### Scenario: Branch falls back to project default

- **WHEN** the branch field is left empty and the resolved project has a configured default branch
- **THEN** the resolved branch is the project's configured default branch

#### Scenario: Submit with an explicit name

- **WHEN** the project resolves, the prompt is non-empty, and the name field holds a non-blank value
- **THEN** the yielded task's name is the trimmed, sanitized name-field value AND the submission signals that the name is user-chosen

#### Scenario: Submit without a name

- **WHEN** the project resolves, the prompt is non-empty, and the name field is blank
- **THEN** the yielded task's name is an auto-generated name derived from the prompt
