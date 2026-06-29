# Forms and Modals

## ADDED Requirements

### Requirement: Profile and archetype selectors on new-agent prompts

The new-agent prompts (new task and new worker) SHALL present a **Profile** cycling selector and an
**Archetype** cycling selector alongside the existing Backend and Model selectors. The new **hera
coordinator** prompt SHALL NOT present an archetype selector (a coordinator is always the `orchestrator`
archetype). The Profile selector SHALL default to the project's bound profile and allow a per-spawn
override; the Archetype selector SHALL offer the canonical archetypes plus a `(none)` option, and SHALL
default to `(none)`. The submitted task SHALL carry the selected archetype (empty when `(none)`), and the
selected profile override (if any) SHALL be passed to the spawn caller.

#### Scenario: Selectors present on the new-task prompt

- **WHEN** the new-task form is opened
- **THEN** it shows a Profile selector defaulting to the project's bound profile and an Archetype
  selector defaulting to `(none)`

#### Scenario: Coordinator prompt omits the archetype selector

- **WHEN** a new hera coordinator prompt is opened
- **THEN** no archetype selector is shown

#### Scenario: Selected archetype rides the submitted task

- **WHEN** the user cycles the Archetype selector to `code_slice` and submits
- **THEN** the produced task carries `code_slice` as its archetype

#### Scenario: None archetype yields an empty value

- **WHEN** the Archetype selector is left on `(none)` and the form is submitted
- **THEN** the produced task carries an empty archetype
