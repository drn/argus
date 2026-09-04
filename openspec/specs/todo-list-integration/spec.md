# todo-list-integration Specification

## Purpose
TBD - created by archiving change add-todo-list-integration. Update Purpose after archive.
## Requirements
### Requirement: Single active to-do backend

The system SHALL support at most one active to-do-list backend at a time, selected by a `backend` config value naming a registered backend. An empty/absent value SHALL mean "no backend configured." Changing the value to a different registered name SHALL fully replace the active backend — the previous backend's tools and connection SHALL be torn down, never left running alongside the new one.

#### Scenario: No backend configured

- **WHEN** the `backend` config value is empty or absent
- **THEN** the system reports no active to-do backend and exposes no to-do tools

#### Scenario: Configuring a backend activates it

- **WHEN** `backend` is set to a registered backend name (e.g. `things3`) with valid backend-specific config
- **THEN** that backend becomes the active backend

#### Scenario: Switching backends replaces, does not add

- **WHEN** an active backend is already configured and `backend` is changed to a different registered name
- **THEN** the previous backend stops being active and only the newly named backend is active afterward — never both

#### Scenario: Unknown backend name is rejected

- **WHEN** `backend` is set to a name that is not a registered backend
- **THEN** the system treats no backend as active and surfaces a clear configuration error rather than crashing

### Requirement: To-do tools track backend configuration live

The MCP server SHALL expose `todo_create`, `todo_list`, `todo_update`, `todo_complete`, and `todo_delete` in `tools/list` if and only if a to-do backend is currently active. This SHALL take effect on the next `tools/list` call after a configuration change, without requiring the daemon to restart.

#### Scenario: Tools absent with no backend

- **WHEN** no to-do backend is configured
- **THEN** `tools/list` does not include any `todo_*` tool, and `tools/call` for a `todo_*` name returns an unknown-tool error

#### Scenario: Tools appear immediately after configuring a backend

- **WHEN** a backend is configured (e.g. via Settings) while the daemon is already running
- **THEN** the very next `tools/list` call includes all five `todo_*` tools, with no daemon restart

#### Scenario: Tools disappear immediately after clearing the backend

- **WHEN** the active backend is cleared (config value set back to empty) while the daemon is already running
- **THEN** the very next `tools/list` call omits all five `todo_*` tools

### Requirement: To-do CRUD tool contracts

Each `todo_*` tool SHALL follow the argument and error conventions already used by `task_*`/`schedule_*` tools: `todo_create` requires a non-empty title and returns the created item including a backend-issued ID; `todo_list` returns open (not completed/canceled) items by default; `todo_update`, `todo_complete`, and `todo_delete` each require the item `id` returned by a prior `todo_create`/`todo_list` call. A call naming an id the active backend does not recognize SHALL return a tool error, not a crash or a silent no-op.

#### Scenario: Create returns a usable id

- **WHEN** `todo_create` is called with a title
- **THEN** the response includes an id that can be passed to `todo_update`/`todo_complete`/`todo_delete` for the same item

#### Scenario: List excludes resolved items by default

- **WHEN** `todo_list` is called with no filter
- **THEN** completed and canceled items are excluded from the result

#### Scenario: Missing id on a mutation is a tool error

- **WHEN** `todo_update`, `todo_complete`, or `todo_delete` is called without an `id`
- **THEN** the tool returns an error and makes no change

#### Scenario: Unknown id is a tool error

- **WHEN** a mutation tool is called with an `id` the backend does not recognize (e.g. already deleted)
- **THEN** the tool returns an error describing the item as not found

### Requirement: No local persistence of to-do items

The system SHALL treat the configured backend as the sole source of truth for to-do items. `todo_list` and item lookups SHALL query the backend live on every call; Argus SHALL NOT cache, mirror, or persist to-do item content in its own database.

#### Scenario: External change is immediately visible

- **WHEN** an item is created, edited, or resolved directly in the backend app (outside Argus)
- **THEN** the next `todo_list`/`todo_update`/`todo_complete`/`todo_delete` call through Argus reflects that change with no separate sync step

### Requirement: Things 3 backend

The system SHALL provide a `things3` backend that implements to-do CRUD by driving the Things 3 macOS app via AppleScript (`osascript`). Created items SHALL be added to a configured destination list (defaulting to Things 3's Inbox when none is configured) and MAY be tagged with a configured tag name. `todo_complete` SHALL set the to-do's status to completed. `todo_delete` SHALL remove the to-do from Things 3. The backend SHALL be unavailable (reported as a configuration error, not a daemon crash) when the host OS is not macOS. Construction succeeding on macOS is a host-OS check only — it does NOT confirm Things 3 is actually installed or running; if it is not, that surfaces as an ordinary per-call tool error on the first `todo_*` call rather than as a configuration-time "unavailable" state.

#### Scenario: Create adds to the configured list

- **WHEN** `todo_create` is called while `things3` is active with a configured destination list
- **THEN** a new to-do is created in Things 3 under that list

#### Scenario: Create with no configured list uses Inbox

- **WHEN** `todo_create` is called while `things3` is active with no destination list configured
- **THEN** the new to-do is created in the Things 3 Inbox

#### Scenario: Complete marks the Things 3 to-do completed

- **WHEN** `todo_complete` is called for an id belonging to an open Things 3 to-do
- **THEN** that to-do's status becomes completed in Things 3

#### Scenario: Backend unavailable on non-macOS

- **WHEN** `backend` is set to `things3` on a non-macOS daemon host
- **THEN** the backend reports a configuration error and does not activate rather than crashing the daemon

#### Scenario: Things 3 not installed surfaces at call time, not configuration time

- **WHEN** `backend` is set to `things3` on a macOS host where Things 3 is not installed or not reachable
- **THEN** the backend still activates (construction only checks the host OS) and the first `todo_*` call fails with an ordinary tool error rather than the backend having been reported unavailable at configuration time

### Requirement: Todo backend configuration in Settings

The Argus TUI Settings view SHALL let the user select the active to-do backend (including "none") from the set of registered backends and edit that backend's config fields, persisting the selection through the same config-value mechanism used by other Settings toggles (e.g. `kb.enabled`). When running against a local daemon (not `--remote`), Settings SHALL validate that a candidate backend can actually construct before persisting the selection, surfacing a failure instead of silently persisting a selection that cannot activate. In `--remote` mode this local validation SHALL be skipped, since the Settings process's own host is not necessarily the remote daemon's host; an unreachable backend selected remotely still degrades gracefully per the "Unknown backend name is rejected" requirement above (tools simply do not appear).

#### Scenario: Selecting a backend in Settings activates it

- **WHEN** the user selects a backend and saves its fields in Settings
- **THEN** the backend becomes active and `todo_*` tools appear on the next `tools/list` call

#### Scenario: Selecting "none" deactivates

- **WHEN** the user selects "none" in Settings
- **THEN** no backend is active and `todo_*` tools no longer appear

#### Scenario: A backend that cannot construct locally is not persisted

- **WHEN** running against a local daemon and the user selects a backend that fails to construct on this host (e.g. a macOS-only backend selected on a non-macOS host)
- **THEN** the selection is not persisted, the previous selection remains active, and the failure is surfaced to the user

#### Scenario: Remote mode skips local validation

- **WHEN** running against a remote daemon (`--remote`) and the user selects a backend
- **THEN** Settings persists the selection without a local construction check, since this process's host is not necessarily the daemon's host

