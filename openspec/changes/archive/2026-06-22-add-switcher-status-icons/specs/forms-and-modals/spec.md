# forms-and-modals

## ADDED Requirements

### Requirement: Task switcher status indicator

The task switcher modal SHALL convey each task's status by rendering the task list's status glyph to the LEFT of the task name, using the same glyph and color the task list shows for that status and (for in-progress tasks) in-progress sub-state. The switcher MUST NOT spell out the status as text to the right of the name. The status glyph mapping MUST be the single shared classifier used by the task list, so the two surfaces cannot drift. The glyph SHALL retain its status color on the selected (highlighted) row; only the name adopts the selected style.

#### Scenario: In-review task shows the status icon, not its name

- **WHEN** the switcher renders a task whose status is `in_review`
- **THEN** the row shows the in-review (clipboard) glyph to the left of the name and the words "In Review" do not appear

#### Scenario: Blocked task shows the needs-input icon

- **WHEN** the switcher renders an in-progress task that is blocked on a user prompt
- **THEN** the row shows the needs-input glyph to the left of the name

#### Scenario: Grouped rows carry the icon indented under their folder

- **WHEN** the grouped (Ctrl+K) switcher renders a task nested under its project header
- **THEN** the status glyph appears indented under the folder, immediately left of the task name, and the project name appears only in the folder header
