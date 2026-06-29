# Settings View

## Purpose

The Settings View is the configuration tab of the argus TUI. It presents a two-panel layout — a left rail of categories and a right pane that renders the selected category's rows and per-row detail. It lets the user inspect and change system, sandbox, project, backend, schedule, knowledge-base, remote-API, appearance, and logging configuration, and renders plugin-registered settings sections (forms and live streams) alongside the built-in categories.
## Requirements
### Requirement: Category rail navigation

The view SHALL present a fixed set of built-in categories (System, Sandbox, Projects, Backends, Schedules, Knowledge Base, Remote API, Appearance, Logs) in a left rail and let the user move focus between the rail and the right pane. Selecting a category SHALL load that category's rows into the pane and reset the row cursor to the top.

#### Scenario: Default focus and category on open

- **WHEN** the Settings View is created
- **THEN** the active category is System and input focus is on the right pane

#### Scenario: Moving focus to the rail and back

- **WHEN** focus is on the pane and the user presses Left (or `h`)
- **THEN** focus moves to the rail
- **WHEN** focus is on the rail and the user presses Right (or `l`) or Enter
- **THEN** focus moves back to the pane

#### Scenario: Selecting a different category

- **WHEN** focus is on the rail and the user moves the rail cursor to another category
- **THEN** the active category changes, the pane renders that category's rows, and the row cursor resets to the first row

#### Scenario: Vim aliases for navigation

- **WHEN** the user presses `j` or `k`
- **THEN** the cursor moves down or up respectively, in either the rail (when rail-focused) or the row list (when pane-focused)

### Requirement: Boolean configuration toggles persist

Pressing Enter on the Sandbox, Knowledge Base, or Remote API status row SHALL flip the corresponding boolean and persist the new value to the configuration store.

#### Scenario: Toggling the sandbox

- **WHEN** the cursor is on the Sandbox status row and the user presses Enter
- **THEN** the sandbox-enabled state inverts and the new value is written to config key `sandbox.enabled`

#### Scenario: Toggling the knowledge base

- **WHEN** the cursor is on the Knowledge Base status row and the user presses Enter
- **THEN** the KB-enabled state inverts and the new value is written to config key `kb.enabled`

#### Scenario: Toggling the remote API

- **WHEN** the cursor is on the Remote API status row and the user presses Enter
- **THEN** the API-enabled state inverts and the new value is written to config key `api.enabled`

### Requirement: Restart-required indication for boot-sensitive settings

For settings that only take effect when the daemon restarts (Metis vault path and Remote API enabled state), the view SHALL capture the value in effect at boot on first refresh and SHALL append a "restart required" indication while the current value differs from the boot value.

#### Scenario: Vault path changed after boot

- **WHEN** the Metis vault path has been changed to a value different from the value recorded at the first refresh
- **THEN** the vault path row is annotated with "(restart required)"

#### Scenario: API enabled state changed after boot

- **WHEN** the Remote API enabled state differs from the value recorded at the first refresh
- **THEN** the Remote API row and detail show "(restart required)"

### Requirement: Project and schedule management actions delegate to callbacks

In the Projects category, the view SHALL fire new/edit/delete/quick-add/apple-events callbacks for the selected project. In the Schedules category, the view SHALL fire new/edit/delete/toggle/run callbacks for the selected schedule. Action keys SHALL be ignored when focus is on the rail or the category does not apply.

#### Scenario: Editing the selected project

- **WHEN** the cursor is on a project row and the user presses `e`
- **THEN** the edit-project callback fires with the selected project's name and config

#### Scenario: Deleting the selected project

- **WHEN** the cursor is on a project row and the user presses `d`
- **THEN** the delete-project callback fires with the selected project's name

#### Scenario: Apple-events key only applies to a project row

- **WHEN** the user presses `a` while focus is on the rail or the active category is not Projects
- **THEN** no callback fires and the key is not consumed

#### Scenario: Toggling a schedule persists its enabled state

- **WHEN** the cursor is on a schedule row and the user presses `t`
- **THEN** the schedule's enabled flag inverts and the change is persisted to the store

#### Scenario: Running a schedule now

- **WHEN** the cursor is on a schedule row and the user presses `r`
- **THEN** the run-schedule callback fires with the schedule's ID

### Requirement: Default backend selection persists

Pressing `d` on a non-default backend row SHALL mark that backend as the default and persist it; pressing `d` on the already-default backend SHALL be a no-op.

#### Scenario: Setting a new default backend

- **WHEN** the cursor is on a backend that is not the current default and the user presses `d`
- **THEN** the default backend becomes that backend and `default_backend` is written to config

#### Scenario: Pressing default on the current default

- **WHEN** the cursor is on the backend already marked default and the user presses `d`
- **THEN** nothing changes

### Requirement: Empty categories show a guiding placeholder

When the Projects, Backends, or Schedules category has no entries, the view SHALL render a single placeholder row directing the user how to add one rather than an empty list.

#### Scenario: No projects

- **WHEN** the Projects category is active and no projects exist
- **THEN** a placeholder row prompting the user to press `n` to add is shown

### Requirement: Inline path editing commits or cancels

The Metis vault path and Argus source path rows SHALL support inline text editing. Pressing Enter SHALL commit the edited buffer and persist it; pressing Escape SHALL abort without persisting. While editing, navigation keys SHALL be consumed so the cursor and tab do not change.

#### Scenario: Committing a vault path edit

- **WHEN** the user edits the Metis vault path and presses Enter
- **THEN** the vault path updates to the edited value and is written to config key `kb.metis_vault_path`

#### Scenario: Cancelling a source path edit

- **WHEN** the user is editing the Argus source path and presses Escape
- **THEN** editing ends and the source path retains its prior value

#### Scenario: Navigation keys are inert while editing

- **WHEN** an inline editor is active and the user presses an arrow key
- **THEN** the keypress is consumed and neither the row cursor nor the active tab changes

### Requirement: Cycling settings via arrow keys

The Appearance spinner row SHALL cycle through spinner styles on Left/Right (and Enter advances forward), persisting the choice. The vault path row SHALL cycle through discovered iCloud vaults on Left/Right; with no discovered vaults the cycle SHALL be a no-op.

#### Scenario: Cycling the spinner style

- **WHEN** the cursor is on the spinner row and the user presses Right
- **THEN** the spinner style advances to the next style and is written to config key `ui.spinner`

#### Scenario: Cycling vault path with no discovered vaults

- **WHEN** the cursor is on the vault path row, no iCloud vaults were discovered, and the user presses Left or Right
- **THEN** the vault path does not change

### Requirement: Logs category renders and scrolls log content

The Logs category SHALL offer a UX Log and a Daemon Log row, render the selected log's lines in the detail pane, scroll on mouse wheel, and reset scroll state when the cursor moves off the log row or switches to a different log.

#### Scenario: Scrolling a log with the wheel

- **WHEN** a log row is selected and the user scrolls the wheel up or down
- **THEN** the log scroll offset decreases or increases (never below zero)

#### Scenario: Switching logs resets scroll

- **WHEN** the cursor moves off the current log row or to a different log
- **THEN** the cached log lines and scroll offset are reset

### Requirement: Daemon-dependent and platform-dependent rows are conditional

System-category rows for daemon restart, Argus source path, and Update Argus SHALL appear only when the daemon is connected. The auto-start-at-login row SHALL appear only on platforms where the launch agent is available, and its label SHALL reflect installed/loaded/busy state. While the daemon is not connected, the view SHALL surface an in-process-mode warning.

#### Scenario: Daemon disconnected hides restart actions and warns

- **WHEN** the daemon connection state is set to disconnected
- **THEN** the System category shows an in-process-mode warning and omits the daemon-restart, source-path, and update rows

#### Scenario: Auto-start row reflects status

- **WHEN** the launch agent is available and installed and loaded
- **THEN** the auto-start row indicates it is enabled

### Requirement: Long-running daemon actions show in-flight state

Triggering Restart Daemon, Update Argus, or the auto-start toggle SHALL mark the action busy, dispatch the work via the corresponding callback, and reflect a busy label until a result is reported back; a second activation while busy SHALL be ignored.

#### Scenario: Updating Argus dispatches once

- **WHEN** the cursor is on the Update Argus row, the daemon is connected, and the user presses Enter
- **THEN** the update callback fires once, the row shows an updating label, and pressing Enter again while updating does not fire the callback a second time

#### Scenario: Result clears busy state

- **WHEN** a self-update result is reported back to the view
- **THEN** the updating flag clears and the reported status and output are shown in the detail pane

### Requirement: Plugin sections render after built-in categories

Plugin-registered settings sections SHALL be hidden entirely when none are registered, and when present SHALL render below a separator and a "Plugins" header, sorted alphabetically by title with ties broken by scope. Neither the separator nor the header SHALL be selectable; rail navigation and rail clicks SHALL skip them.

#### Scenario: No plugins registered

- **WHEN** there are no registered plugin sections
- **THEN** no "Plugins" header, separator, or plugin entries appear in the rail

#### Scenario: Plugins shown and ordered

- **WHEN** plugin sections are registered
- **THEN** they appear after the built-in categories under a "Plugins" header, ordered alphabetically by title

#### Scenario: Navigation skips non-selectable rail rows

- **WHEN** the rail cursor moves across the separator and "Plugins" header
- **THEN** the cursor lands on a selectable category or plugin entry, never on the separator or header

### Requirement: Plugin form fields are editable and submittable

A selected form-type plugin section SHALL render one row per declared field plus a Save row. Bool fields toggle on Enter or Left/Right; enum fields cycle on Enter or Left/Right; string and int fields open an inline editor on Enter. Int edits SHALL reject non-numeric input (keeping the editor open) and clamp committed values to any declared min/max. Pressing Save SHALL gather every field's current draft value (falling back to its default when untouched) and dispatch them to the section's submit hook, recording the result status.

#### Scenario: Toggling a bool field

- **WHEN** the cursor is on a bool field row and the user presses Enter
- **THEN** the field's draft value inverts

#### Scenario: Cycling an enum field

- **WHEN** the cursor is on an enum field row and the user presses Right (or Left)
- **THEN** the draft value advances (or retreats) to the adjacent option, wrapping around the option list

#### Scenario: Int edit clamps to max

- **WHEN** the user edits an int field with a declared max and commits a value above the max
- **THEN** the stored draft value is clamped to the max

#### Scenario: Int edit rejects non-numeric input

- **WHEN** the user commits a non-numeric value in an int field editor
- **THEN** the value is not committed and the editor stays open

#### Scenario: Saving submits all field values

- **WHEN** the user presses Enter on the Save row
- **THEN** the submit hook receives a value for every field (untouched fields contribute their defaults) and the resulting status (success or failure) is recorded for display

#### Scenario: Save with no submitter wired

- **WHEN** the Save row is activated and no submit hook is configured
- **THEN** a "saved (no submitter)" status is recorded and no error is raised

### Requirement: Plugin stream sections render a live pane and signal focus transitions

A stream-type plugin section SHALL contribute no field rows; instead the right pane SHALL be devoted to a live stream pane. The view SHALL fire a focus signal when a stream section gains focus and a blur signal when it loses focus (category change, focus to rail, or the section being unregistered), and SHALL preserve the stream pane's received content across focus toggles.

#### Scenario: Focusing and blurring a stream section

- **WHEN** a stream section gains focus and then the active section changes away from it
- **THEN** the stream-focus signal fires on entry and the stream-blur signal fires on exit

#### Scenario: Content preserved across re-entry

- **WHEN** the user leaves a stream section and returns to it
- **THEN** the same stream pane (with its previously received content) is reused rather than recreated

#### Scenario: Unregistering the focused stream blurs it

- **WHEN** a stream section currently holding focus is unregistered on refresh
- **THEN** its stream pane is closed and the stream-blur signal fires

### Requirement: Stale plugin state is pruned and recovered

On refresh, the view SHALL drop draft values and submit statuses for plugin sections that are no longer registered. If the active category is a plugin section that has been unregistered, rail navigation SHALL recover by resetting the active category to System.

#### Scenario: Drafts pruned for unregistered plugin

- **WHEN** a refresh occurs and a previously registered plugin section is gone
- **THEN** that section's draft values and submit status are removed

#### Scenario: Navigation recovers from a stale active plugin

- **WHEN** the active category points at a plugin section no longer present and the user attempts to move the rail cursor
- **THEN** the active category resets to System

### Requirement: Inline edits accept pasted text

While an inline editor is active (vault path, source path, or plugin string/int field), pasted text SHALL be appended to the corresponding edit buffer.

#### Scenario: Pasting into a plugin field editor

- **WHEN** a plugin string field editor is active and text is pasted
- **THEN** the pasted text is appended to that field's edit buffer

### Requirement: Settings data degrades gracefully on load errors

Refreshing settings data SHALL tolerate failures loading projects, tasks, schedules, or plugin sections by treating the failed dataset as empty rather than crashing.

#### Scenario: Projects fail to load

- **WHEN** the projects datastore returns an error during refresh
- **THEN** the Projects category renders as an empty list (its placeholder row) instead of failing

### Requirement: Project profile selection from validated on-disk profiles

The Settings project view SHALL present the project's bound profile as a select-list populated from the
profiles discoverable on disk (the per-user library and any in-repo directory). Only profiles that pass
validation SHALL be selectable; invalid profiles SHALL be shown as non-selectable (or excluded) so the
operator cannot bind a project to a malformed profile. Selecting a profile SHALL persist its name on the
project; the profile body SHALL NOT be persisted.

#### Scenario: Valid profiles are offered

- **WHEN** the project view is opened and the disk holds a mix of valid and invalid profiles
- **THEN** the select-list offers the valid profiles and the currently bound name

#### Scenario: Invalid profiles are not selectable

- **WHEN** a profile on disk fails validation
- **THEN** it cannot be chosen as a project's binding

#### Scenario: Selection persists the name only

- **WHEN** the operator selects a profile for a project
- **THEN** the project's stored binding is the profile name, and no profile body is written

