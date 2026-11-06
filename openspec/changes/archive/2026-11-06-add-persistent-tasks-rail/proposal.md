## Why

Opening a task currently replaces the Tasks list with the agent view, making switching between tasks interrupt the view of the wider project. An optional persistent rail lets users browse tasks grouped by project while keeping the selected agent beside them, like the Projects tab.

## What changes

- Add `ui.tasks_rail = false` and a live “Persistent task rail” toggle under Settings → Appearance.
- When enabled, show a 34-column Tasks rail beside the selected task's existing agent view. Keep the rail visible when zooming the agent content.
- Group tasks by project with selectable, independently focusable headers, retaining task/project status indicators, needs-input markers, spinners, pinned tasks, archive, and the `H` visibility filter.
- Rail navigation previews existing sessions or recordings without starting them; Enter enters the selected terminal using the existing start/resume and close-out checks.
- Mirror Projects focus navigation: Tab enters from the rail, terminal Tab passes through, modified Left/Right move between regions, and Ctrl+Q returns to the rail.
- Preserve the existing Tasks list and full-page agent behavior when the setting is off.

## Capabilities

### New capabilities

- `persistent-task-rail`: Optional Tasks rail layout, terminal selection, focus routing, and live transitions.

### Modified capabilities

- `task-list-view`: Scope the existing automatic folder/cursor/archive and three-panel behavior to the default layout; define the opt-in rail's folder behavior.
- `settings-view`: Persist and apply the new Appearance preference.

## Impact

- TUI Tasks layout and input routing, task-list rendering, agent session binding and PTY sizing, UI config persistence/import, Settings, keymap/help, tests, and README Reference.
- Reuse `TaskListView`'s grouping/filter/section/status machinery and the existing terminal panes; share small rail/focus/layout primitives with Projects where useful instead of adapting tasks into Hera roles.
- No REST wire change or new dependency.

## Non-goals

- **Named follow-up: Web SPA persistent project task rail.** The web Tasks view remains unchanged; a future change can add equivalent navigation using existing task endpoints.
- **Named follow-up: macOS persistent project task rail.** The native Tasks view remains unchanged; a future change can add the matching sidebar preference and layout.
- Redesigning the Projects rail, replacing task status semantics with Hera role statuses, or changing behavior with the preference off.
