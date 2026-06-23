# forms-and-modals (delta)

## ADDED Requirements

### Requirement: Copy-choice modal

The copy-choice modal SHALL present a list of clipboard targets for a task and report the user's selection. It SHALL always offer a **Name** choice, and SHALL offer a **Prompt** choice only when the task's prompt is non-empty. Up/Down (and `j`/`k`) SHALL move the selection cursor among the available choices; Enter SHALL confirm the highlighted choice; Esc or Ctrl+Q SHALL cancel without selecting. The modal SHALL expose its outcome (selected target, or canceled) for the caller to act on.

#### Scenario: Prompt choice hidden when prompt is empty

- **WHEN** the modal is built for a task whose prompt is empty
- **THEN** only the Name choice is offered and it is the highlighted choice

#### Scenario: Selecting a choice reports it

- **WHEN** the user highlights the Prompt choice and presses Enter
- **THEN** the modal reports the Prompt choice as selected and not canceled

#### Scenario: Cancel reports no selection

- **WHEN** the user presses Esc
- **THEN** the modal reports canceled and no choice selected
