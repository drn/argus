## Why

The new-task and new-coordinator popups start at the prompt with a remembered project, so correcting a wrong project requires traversing many fields twice. An optional project question before the full form lets users confirm the destination before writing the prompt.

## What changes

- Add `ui.project_first`, default false, with a persistent “Project first” toggle in TUI Settings → Appearance.
- When enabled and more than one project is available, show only the existing project typeahead interaction before the task or coordinator form. Preserve today's entry-point-specific default project.
- Enter accepts a valid project through the full form's existing project-change path, reveals the existing full form, and focuses the prompt. Keep its project field editable.
- Escape cancels the entire popup without creating anything or carrying temporary selection into another opening.
- Skip the question for a single project or an entry point with a fixed project. With the preference disabled, preserve existing defaults, focus, and navigation exactly.

## Capabilities

### New capabilities

- `project-first-task-form`: Optional project confirmation before TUI task and coordinator creation, including interaction, default resolution, cancellation, and entry-point scope.

### Modified capabilities

- `settings-view`: Add the persistent Project first preference under Appearance.
- `config-management`: Add the default-off UI preference and configuration-store loading.

## Impact

- TUI `NewTaskForm`, shared form construction/opening, Settings, UI config, and DB config loading. No REST endpoint, event, or task schema change.
- Unit tests and real-event-loop SimulationScreen smoke tests for both preference states, defaults, cancellation, entry-point coverage, and persistence.
- UX logging for project-step transitions and preference persistence; document any non-obvious lifecycle and async-result invariants.

## Non-goals

- **Web follow-up: project-first web task creation.** Evaluate an equivalent opt-in destination-first flow in the web SPA separately; this change addresses TUI keyboard traversal and does not alter web creation or settings.
- **macOS follow-up: project-first macOS task creation.** Evaluate an equivalent opt-in destination-first flow in the native app separately; this change does not alter macOS creation or settings.
- **Hera worker spawn:** Keep the rail's `w` action and its command-palette equivalent on the current full-form flow. They begin in the selected coordinator's project and create a worker under that coordinator rather than an independent task/coordinator. The full form's project field remains editable as today.
- Do not change project-default policy, add new global keys, or redesign the full form. Reuse project-change resolution rather than introducing separate backend/account/branch/sandbox default rules.

## Entry-point inventory

- Tasks-tab new task: `TaskListView.OnNew` → `App.onNewTask` → `buildNewTaskForm`, reached by the configurable new-task key and the command palette's task action. Include.
- Hera new root coordinator: `HeraPage.OnNewCoordinator` → `App.heraNewCoordinator` → `openHeraNewTaskForm`, reached by the configurable new-coordinator key and the command palette's Hera action. Include, including an empty rail selection; selected coordinator project is a default, not a fixed destination.
- Hera worker spawn: `HeraPage.OnSpawnWorker` → `App.heraSpawnWorker` → `openHeraNewTaskForm`, also exposed through the command palette. Exclude as described above.
- `NewNewTaskForm` has no other production constructor callers beyond `buildNewTaskForm`. REST/headless/MCP creation does not use this popup and is outside this TUI-only preference.
