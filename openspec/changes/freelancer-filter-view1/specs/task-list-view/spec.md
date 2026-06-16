# task-list-view

## ADDED Requirements

### Requirement: Freelancers-only filter toggle

The task list SHALL provide a "freelancers-only" filter toggled by the `f` key. While active, the list SHALL display only freelancer tasks and SHALL hide every hera-managed task; pressing `f` again SHALL restore the full list. The toggle SHALL default to off (full list shown). The filter SHALL compose with the substring filter (`/`) and the hide-hera-workers toggle (`H`) — each is an independent exclusion applied in the same row-build pass.

A task SHALL be classified as **hera-managed** when it holds at least one live hera binding (a binding whose `ended_at` is unset) to a role of kind `coordinator` or `worker`. A task SHALL be classified as a **freelancer** otherwise: when it has no live binding, or holds only `freelance`-kind live bindings. The classification SHALL be derived from the hera bindings/roles store, NOT from the `task_meta` `hera` sidecar (which is not cleared when a binding ends and therefore misclassifies finished workers).

When the freelancers-only filter is active, the task list panel SHALL show an unambiguous indicator (distinct from the substring-filter indicator) so it is evident the list is filtered to freelancers.

#### Scenario: Toggling on hides managed tasks

- **WHEN** the freelancers-only filter is off and the user presses `f`
- **THEN** every task holding a live coordinator- or worker-kind binding is hidden, and only freelancer tasks remain visible

#### Scenario: Toggling off restores the full list

- **WHEN** the freelancers-only filter is active and the user presses `f`
- **THEN** the filter deactivates and previously hidden managed tasks become visible again

#### Scenario: Finished worker becomes a freelancer

- **WHEN** a task's only hera binding was a worker binding that has since ended (its `ended_at` is set) and the freelancers-only filter is active
- **THEN** the task is treated as a freelancer and remains visible

#### Scenario: Freelance-kind binding is not managed

- **WHEN** a task holds only a live `freelance`-kind binding and the freelancers-only filter is active
- **THEN** the task is treated as a freelancer and remains visible

#### Scenario: Coordinator task is hidden

- **WHEN** a task holds a live coordinator-kind binding and the freelancers-only filter is active
- **THEN** the task is hidden

#### Scenario: Active filter shows an indicator

- **WHEN** the freelancers-only filter is active
- **THEN** the task list panel renders a freelancers-only indicator distinct from the substring-filter indicator

#### Scenario: Composes with the substring filter

- **WHEN** both the freelancers-only filter and a substring filter are active
- **THEN** a task is visible only if it is a freelancer AND matches every substring term
