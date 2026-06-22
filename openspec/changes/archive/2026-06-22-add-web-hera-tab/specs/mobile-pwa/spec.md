# Mobile PWA

## ADDED Requirements

### Requirement: Hera orchestration tab

The PWA SHALL provide a read-only "Hera" tab (the second tab, reachable by the `g` hotkey and documented in the in-app help modal) that renders the orchestration roster from `GET /api/hera`. Orchestrators SHALL be grouped into Pinned, Active, and Archived sections, with a separate Freelance section for hoisted freelance roles. Each role row SHALL show a status dot keyed to the hera status, the role kind and name, and — when the role holds a live binding — the bound task's name and workflow badge plus a ready-to-close indicator when flagged. Tapping a role that has a live binding SHALL open that task's existing detail/terminal overlay; the roster itself SHALL remain read-only (no mutation controls). All orchestrator, role, and task names rendered into the DOM MUST be HTML-escaped.

#### Scenario: Switching to the Hera tab renders the roster
- **WHEN** the user presses `g` or taps the Hera tab
- **THEN** the Hera view is shown and the roster is fetched and rendered, with a status line summarizing the orchestrator and role counts

#### Scenario: Empty roster placeholder
- **WHEN** `/api/hera` returns no orchestrators and no freelance roles
- **THEN** the Hera view shows an empty-state placeholder rather than an error

#### Scenario: Drill into a live role
- **WHEN** the user taps a role row that has a live binding
- **THEN** the task detail overlay opens for that role's bound task

#### Scenario: Roster excluded from mutation
- **WHEN** the Hera tab is displayed
- **THEN** it exposes no controls that create, modify, or delete orchestrators or roles

### Requirement: Hera tab participates in the connection lifecycle

While the Hera tab is foregrounded, the periodic poll SHALL refresh the roster instead of the task list, and the roster fetch SHALL drive the same connection-tracking the task-list refresh does — updating the connection dot and the consecutive-failure count that promotes the offline view — so a daemon that becomes unreachable while the user is on the Hera tab is still detected. On reconnect (the retry action and the browser `online` event) the roster SHALL be refreshed when the Hera tab is the active tab.

#### Scenario: Offline detection on the Hera tab
- **WHEN** the daemon becomes unreachable while the Hera tab is foregrounded and the roster fetch fails repeatedly past the failure threshold
- **THEN** the connection dot turns to its error state and the offline view is shown, exactly as it would be from the task-list poll

#### Scenario: Successful fetch clears failure state
- **WHEN** a roster fetch succeeds
- **THEN** the consecutive-failure count is reset and the offline view is hidden if it was showing

#### Scenario: Roster refreshes on reconnect
- **WHEN** the browser fires the `online` event (or the user taps retry) while the Hera tab is active
- **THEN** the roster is refreshed rather than left stale until the next poll tick
