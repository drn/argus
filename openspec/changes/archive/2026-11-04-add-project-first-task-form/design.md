## Context

`App.buildNewTaskForm` is the only production constructor path. Tasks creation opens it through `onNewTask`; Hera coordinator and worker creation share `openHeraNewTaskForm`. The form already starts at the prompt and owns project typeahead, paste handling, and `onProjectChanged` for branch/profile/account/skills refresh. The opt-in flow must preserve that single owner of project-dependent behavior.

## Goals / Non-goals

**Goals:**

- Add a project-only phase to the same form, preserving its existing typeahead and default-resolution code.
- Keep the disabled path and the full form's navigation unchanged.
- Use the existing UI config and Settings persistence conventions.

**Non-goals:**

- Worker-spawn flow changes, web/macOS creation changes, new REST surfaces, or different project-default rules. Named frontend follow-ups and all entry points are listed in the proposal.

## Decisions

- Represent the project question as an opt-in phase of `NewTaskForm`, rather than a separate picker widget. Reuse project input, matching, candidate navigation, drawing, and `PasteHandler`. Start with an empty filter and list open; highlight the default or first row. Input changes reset the highlight to the first match. Enter accepts a candidate and advances; Escape cancels before candidate-dismiss handling.
- Keep a separate project-step ordering: entry-point default first, then maximum `Task.CreatedAt` per project from the existing task list, then alphabetical ties/unused projects. Preserve ordering through filtering and leave the full field alphabetical; add no storage.
- Remember whether the popup passed through the project step. Escape from that full form closes autocomplete first, then returns to an empty-filter project step highlighting the current project; preserve prompt/name. Ctrl+Q cancels outright for this flow. Disabled and bypassed popups retain existing behavior.
- Activate that phase at the task/coordinator opening sites with explicit scope, rather than unconditionally in `buildNewTaskForm`. The shared builder also serves worker spawn, which is deliberately excluded. Single/zero-project cases bypass the phase.
- Run acceptance through the same project-change helper used by the full form, then clear the candidate popup and focus the prompt. Do not duplicate dependent-default rules or alter backend/account/sandbox semantics. Compare both selection routes in tests.
- Add `UI.ProjectFirst` / `ui.project_first` and a Settings Appearance row alongside the existing UI toggles. Read the effective config when opening a popup, avoiding a second cached preference in App.
- Apply only for a local DB; remote creation always bypasses the phase. Match sibling remote Appearance toggles' cosmetic/log-only persistence failure behavior. Remote support is a named follow-up (coordinator decision, message 20932), with no REST changes.

## Risks / Trade-offs

- A shared form phase can accidentally alter disabled focus navigation → real-event-loop smoke tests pin both states and unit tests pin the full-form field order.
- Candidate Enter handling can consume Enter without advancing, or advance without applying defaults → test one Enter with both a preselected project and a different typeahead candidate.
- Canceled/replaced forms can receive old async branch results → apply results only to the originating live form and relevant project; log stale skips. Add a regression test if the shared loader requires correction.
- The setting adds one extra Enter when the default project is correct → default false and bypass when there is no project choice.
