# Project-first task form

## Purpose

Define the optional project question before local TUI task and coordinator creation, including filtering, recent-project ordering, shared default resolution, backtracking, cancellation, and bypass behavior.

## Requirements

### Requirement: Optional project question precedes independent creation

When `ui.project_first` is true and multiple projects are configured, the TUI SHALL open a project-only step for new independent tasks and new root coordinators. Its filter SHALL start empty with the candidate list open and the existing entry-point default highlighted (or the first row if no valid default exists). It SHALL reuse existing project matching, text editing, bracketed paste, and candidate navigation. Typing SHALL filter the candidates and highlight the first match, without requiring deletion of the default. Enter SHALL accept the highlighted row in one press. This behavior SHALL NOT alter the full form's own project field.

#### Scenario: Tasks entry preserves the selected project

- **WHEN** the preference is enabled, multiple projects exist, and the user invokes Tasks new task by key or command palette
- **THEN** only the project question and its selection UI are shown, with an empty filter and the Tasks default highlighted

#### Scenario: Coordinator entry preserves its contextual default

- **WHEN** the preference is enabled, multiple projects exist, and the user invokes Hera new coordinator by key or command palette
- **THEN** only the project question is shown, with an empty filter and the selected coordinator task's project highlighted when resolvable, otherwise the last-selected Tasks project (or the first candidate)
- **AND** an empty Hera rail selection still allows new-coordinator creation

#### Scenario: Typeahead and paste select a project

- **WHEN** the user edits or pastes a project name on the project step and navigates the matching candidates
- **THEN** existing project matching filters the ordered candidates, the first match is highlighted, and Up/Down navigate those candidates

#### Scenario: Typing replaces the default selection

- **WHEN** a non-empty default project is highlighted and the user types another project's name without backspacing
- **THEN** the empty filter receives that name and Enter selects the matching project and advances in one press

### Requirement: Project candidates prioritize default and recent use

The project step SHALL order the entry-point default first, other projects by most recent task creation timestamp, and projects with no task history alphabetically. Equal timestamps SHALL use alphabetical order. Recency SHALL derive from existing task data without new persisted state. Filtering SHALL preserve this ordering. The full form's project field SHALL retain its existing alphabetical ordering.

#### Scenario: Recent projects precede unused projects

- **WHEN** project task history is available and the project step opens or filters
- **THEN** the entry-point default is first when matching, followed by other matching projects in descending most-recent-task order, then unused matching projects alphabetically

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

### Requirement: Escape returns to project selection while preserving writing

For popups that passed through the enabled project step, Escape in the full form SHALL return to the project step while preserving the typed prompt and optional name. If autocomplete is open, the first Escape SHALL close it as today. Returning SHALL empty the filter, open the list in its original order, and highlight the current project. Re-acceptance SHALL reuse existing project-change resolution and override semantics. Ctrl+Q SHALL cancel outright. Popups that did not pass through the project step SHALL retain existing Escape and Ctrl+Q behavior.

#### Scenario: Correct a wrong project after writing

- **WHEN** the user accepts a project, writes a prompt and optional name, then presses Escape with no autocomplete open
- **THEN** the project step returns with an empty filter and current project highlighted
- **AND** accepting another project returns to prompt focus with prompt and name preserved and dependent values re-resolved

#### Scenario: Escape closes autocomplete before returning

- **WHEN** autocomplete is open in a full form reached through the project step and the user presses Escape
- **THEN** only the autocomplete closes, and a subsequent Escape returns to project selection

#### Scenario: Cancel from the full form

- **WHEN** a full form reached through the project step has no autocomplete open
- **THEN** Escape followed by Escape cancels the popup, and Ctrl+Q cancels it outright

### Requirement: Existing flow is preserved when the question is unnecessary

When `ui.project_first` is false, every form entry point SHALL preserve its existing behavior, defaults, focus order, and initial prompt focus. When enabled but only one project exists or the entry point fixes a project, the TUI SHALL skip the project question and show the full form with prompt focus. With zero projects, the existing no-project form and validation behavior SHALL remain available without a project-only dead end. Hera rail worker spawn SHALL remain on its existing full-form flow regardless of this preference.

Remote TUI (`--remote`) SHALL always skip the project question; remote preference support is a named follow-up without REST changes in this change.

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

#### Scenario: Remote creation skips the project question

- **WHEN** the remote TUI opens the new-task popup, even if the daemon's config snapshot enables `ui.project_first`
- **THEN** the existing full form opens directly with its existing defaults, focus, and navigation
