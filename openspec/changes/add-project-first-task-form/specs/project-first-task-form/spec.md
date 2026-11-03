## ADDED Requirements

### Requirement: Optional project question precedes independent creation

When `ui.project_first` is true and multiple projects are configured, the TUI SHALL open a project-only step for new independent tasks and new root coordinators. It SHALL initially select the same project that the existing full form would default to for that entry point. The step SHALL reuse the full form's project selection interaction, including typeahead, candidate navigation, editing, and bracketed paste.

#### Scenario: Tasks entry preserves the selected project

- **WHEN** the preference is enabled, multiple projects exist, and the user invokes Tasks new task by key or command palette
- **THEN** only the project question and its selection UI are shown, initialized from the Tasks selection exactly as the existing full form would be

#### Scenario: Coordinator entry preserves its contextual default

- **WHEN** the preference is enabled, multiple projects exist, and the user invokes Hera new coordinator by key or command palette
- **THEN** only the project question is shown, defaulting to the selected coordinator task's project when resolvable, otherwise the last-selected Tasks project
- **AND** an empty Hera rail selection still allows new-coordinator creation

#### Scenario: Typeahead and paste select a project

- **WHEN** the user edits or pastes a project name on the project step and navigates the matching candidates
- **THEN** matching and selection follow the existing full-form project field's interaction

### Requirement: Project acceptance reuses full-form default resolution

Enter on the project step SHALL accept a valid selected project, reveal the full existing form, and focus its prompt input without submitting a task. Acceptance SHALL reuse the full form's own project-change code path. Branch, backend/model, profile, account, sandbox, skills, and other project-dependent values SHALL resolve exactly as they do when choosing that project in the full form, including inherited defaults and existing override semantics. The project field SHALL remain editable in the full form.

#### Scenario: Accept a different project

- **WHEN** the user selects a different configured project and presses Enter
- **THEN** the full form appears with that project, focus is in the prompt, and its dependent values equal those produced by selecting the same project through the full form's project field
- **AND** no task or coordinator is created until the full form is submitted

#### Scenario: Accept the initial project

- **WHEN** the initial default is a valid project and the user presses Enter without editing it
- **THEN** the full form appears with that project and prompt focus, applying the same project-change resolution path

#### Scenario: Invalid project cannot advance

- **WHEN** the project text does not resolve to a configured project and there is no valid selected candidate to accept
- **THEN** Enter keeps the user on the project step without creating a task

#### Scenario: Project remains editable

- **WHEN** the user reaches the full form and changes its project field again
- **THEN** the existing project interaction and default resolution continue to work

### Requirement: Project-step cancellation leaves no pending creation state

Escape on the project step SHALL cancel the whole popup immediately, including when its project candidates are open, and restore the originating page and focus. It SHALL leave no task, coordinator, worktree, pending submit callback, or remembered temporary project selection behind. Async results from the canceled opening SHALL NOT mutate a subsequent form.

#### Scenario: Cancel and reopen

- **WHEN** the user changes the project-step selection, presses Escape, and opens the popup again
- **THEN** the original page is restored on cancel and the reopened popup uses the normal entry-point default rather than the canceled selection

### Requirement: Existing flow is preserved when the question is unnecessary

When `ui.project_first` is false, every form entry point SHALL preserve its existing behavior, defaults, focus order, and initial prompt focus. When enabled but only one project exists or the entry point fixes a project, the TUI SHALL skip the project question and show the full form with prompt focus. With zero projects, the existing no-project form and validation behavior SHALL remain available without a project-only dead end. Hera rail worker spawn SHALL remain on its existing full-form flow regardless of this preference.

#### Scenario: Default-off compatibility

- **WHEN** the setting is absent or false and the user opens a task or coordinator popup
- **THEN** the existing full form opens immediately with its existing defaults and prompt focus, and field navigation is unchanged

#### Scenario: Single-project configuration

- **WHEN** the preference is enabled and exactly one project exists
- **THEN** the full form opens directly with that project and prompt focus

#### Scenario: No configured projects

- **WHEN** the preference is enabled and no projects exist
- **THEN** the existing full form opens directly and preserves existing project validation

#### Scenario: Coordinator-scoped worker creation

- **WHEN** the preference is enabled and the user invokes Hera worker spawn
- **THEN** the existing full form opens directly, defaulted to the coordinator's project, with prompt focus and its project field still editable
