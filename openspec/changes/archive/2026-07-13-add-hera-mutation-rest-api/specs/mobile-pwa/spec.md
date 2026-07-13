## RENAMED Requirements

- FROM: `### Requirement: Hera orchestration tab`
- TO: `### Requirement: Hera orchestration tab with spawn, send, and plan mutations`

## MODIFIED Requirements

### Requirement: Hera orchestration tab with spawn, send, and plan mutations

The PWA SHALL provide a "Hera" tab (the second tab, reachable by the `g` hotkey and documented in the in-app help modal) that renders the orchestration roster from `GET /api/hera`, grouped into Pinned, Active, and Archived sections plus a Freelance section, as before. Each role row SHALL show a status dot, kind, name, and — when live-bound — the bound task's name, workflow badge, and ready-to-close indicator. Tapping a live-bound role SHALL open that task's existing detail/terminal overlay.

The tab SHALL additionally provide mutation controls, calling the REST endpoints under `/api/hera/orchestrators/{orch_id}/...` and acting as the target orchestrator's coordinator in every case:

- **Spawn worker** — a form (prompt required; role name, project, branch, backend, model optional) reachable from a coordinator/orchestrator card, calling `POST .../workers`.
- **Send message** — a compose form (recipient picker sourced from the already-rendered roster, body, tldr) reachable from a coordinator card, calling `POST .../messages`. No sender-role selector is shown — sends are always attributed to the orchestrator's coordinator.
- **Plan mutations** — create/edit/cancel a planned node and add/remove a blocking edge between two roles, reachable from an orchestrator's plan view, calling the `plan/nodes` and `plan/blocks` endpoints.

All orchestrator, role, and task names, plus any user-entered mutation input (prompt, message body, tldr) rendered back into the DOM, MUST be HTML-escaped. `hera_join`/`hera_move` remain unexposed — out of scope for this change.

#### Scenario: Switching to the Hera tab renders the roster

- **WHEN** the user presses `g` or taps the Hera tab
- **THEN** the Hera view is shown and the roster is fetched and rendered, with a status line summarizing the orchestrator and role counts

#### Scenario: Empty roster placeholder

- **WHEN** the roster has no orchestrators and no freelance roles
- **THEN** the Hera view shows an empty-state placeholder rather than an error

#### Scenario: Drill into a live role

- **WHEN** the user taps a role row with a live-bound task
- **THEN** the app opens that task's existing detail/terminal overlay

#### Scenario: Spawn worker from the web app

- **WHEN** the user submits the spawn-worker form with a prompt for a coordinator card
- **THEN** the app calls `POST /api/hera/orchestrators/{orch_id}/workers` and re-runs `loadHera()` on success so the new worker appears

#### Scenario: Send message from the web app

- **WHEN** the user submits the send-message form with a recipient, body, and tldr
- **THEN** the app calls `POST /api/hera/orchestrators/{orch_id}/messages`, with no sender-role selector shown to the user

#### Scenario: Cancel a planned node requires confirmation

- **WHEN** the user chooses to cancel a planned node
- **THEN** the app shows a confirmation prompt before calling `POST .../plan/nodes/{role_id}/cancel`

#### Scenario: Mutation input is escaped

- **WHEN** a spawn-worker prompt or send-message body contains HTML-significant characters
- **THEN** any subsequent render of that value into the roster (e.g. a node's displayed prompt) is HTML-escaped

#### Scenario: Join/move remain unavailable

- **WHEN** the user views the Hera tab
- **THEN** the app presents no control to re-bind a task's orchestrator membership

### Requirement: Hera tab participates in the connection lifecycle

While the Hera tab is foregrounded, the periodic poll SHALL refresh the roster instead of the task list, and the roster fetch SHALL drive the same connection-tracking the task-list refresh does — updating the connection dot and the consecutive-failure count that promotes the offline view — so a daemon that becomes unreachable while the user is on the Hera tab is still detected. On reconnect (the retry action and the browser `online` event) the roster SHALL be refreshed when the Hera tab is the active tab. A successful mutation SHALL trigger the same roster refresh path as the poll tick, rather than a separate ad hoc re-render.

#### Scenario: Offline detection on the Hera tab

- **WHEN** the daemon becomes unreachable while the Hera tab is foregrounded and the roster fetch fails repeatedly past the failure threshold
- **THEN** the offline view is shown

#### Scenario: Successful fetch clears failure state

- **WHEN** a roster fetch succeeds after prior failures
- **THEN** the consecutive-failure count resets and the connection dot shows connected

#### Scenario: Roster refreshes on reconnect

- **WHEN** the browser fires the `online` event (or the user taps retry) while the Hera tab is active
- **THEN** the roster is refreshed

#### Scenario: A successful mutation refreshes the roster

- **WHEN** a spawn-worker, send-message, or plan-mutation request succeeds
- **THEN** the app refreshes the roster via the same `loadHera()` path the poll tick uses, so the mutation's effect is visible without a manual reload
