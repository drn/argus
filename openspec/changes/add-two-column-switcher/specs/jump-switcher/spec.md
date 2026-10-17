## ADDED Requirements

### Requirement: Jump switcher opens from any focus via ctrl+j

The system SHALL open the jump switcher popup when the `global.switcher` action (default `ctrl+j`) is pressed in any of these contexts: the classic agent view (zoomed or not), the plain Tasks tab, the Projects rail, and a focused Projects coordinator or agent pane (including fullscreen). The key SHALL be consumed and MUST NOT reach any agent PTY. The popup SHALL NOT open while a form or modal already holds the keyboard, nor while a plugin view is active, and pressing the key while the popup is already open SHALL have no effect.

#### Scenario: Opens from the classic agent view, zoomed and unzoomed

- **WHEN** the user presses `ctrl+j` while viewing a task in the classic agent view, with zoom on or off
- **THEN** the jump switcher popup opens and the agent PTY does not receive the key

#### Scenario: Opens from the Tasks tab and the Projects tab

- **WHEN** the user presses `ctrl+j` with the Tasks tab list focused, the Projects rail focused, or a Projects coordinator or agent pane focused
- **THEN** the jump switcher popup opens in every case and no pane PTY receives the key

#### Scenario: Does not open over a form or a plugin view

- **WHEN** the user presses `ctrl+j` while the new-task form is open, or while a plugin view is active
- **THEN** the jump switcher does not open and the key goes to the form or plugin as before

### Requirement: Jump switcher shows the Tasks list and the Projects rail side by side

The popup SHALL show two columns: the Tasks list on the left and the Projects rail on the right. Each column SHALL be rendered by its own private instance of the corresponding real widget, so rows, folding, status glyphs, and `(?)` markers look the same as on the real tabs. The Tasks column SHALL never show hera-managed tasks (workers or coordinators), regardless of the Tasks tab's hide-hera-managed toggle, and SHALL NOT show archived tasks. The popup SHALL open with each column's folds seeded from the corresponding real tab. In remote mode the Projects column SHALL show an unavailable placeholder and SHALL be skipped by column switching and needs-input cycling.

#### Scenario: Tasks on the left, Projects on the right

- **WHEN** the popup opens
- **THEN** the left column lists plain tasks grouped by project and the right column shows the Projects rail tree

#### Scenario: Hera-managed tasks never appear in the Tasks column

- **WHEN** the Tasks tab's hide-hera-managed toggle is off and the popup opens
- **THEN** no hera-managed worker or coordinator task appears in the Tasks column

#### Scenario: Archived tasks are excluded

- **WHEN** an archived task exists and the popup opens
- **THEN** that task does not appear in the Tasks column

### Requirement: Jump switcher navigation is private to the popup

Up/Down SHALL move the cursor within the active column using that widget's own row-skipping and fold rules. Left/Right SHALL switch the active column, and each column SHALL keep its own cursor. Space SHALL toggle the fold of the selected row in the Projects column. Navigating or folding in the popup MUST NOT change the real Tasks tab or Projects rail cursor or fold state.

#### Scenario: Arrows move within and between columns

- **WHEN** the user presses Down, then Right, then Left
- **THEN** the cursor moves one row down in the active column, then the Projects column becomes active, then the Tasks column becomes active again with its cursor where it was left

#### Scenario: Real rails are untouched

- **WHEN** the user moves the cursor and toggles folds inside the popup and then closes it with Esc
- **THEN** the real Tasks tab and Projects rail have the same cursor and fold state they had before the popup opened

### Requirement: Typing filters both columns immediately

Every printable character other than Space SHALL append to a single shared filter query, applied to both columns on each keystroke using each widget's existing match rules. Backspace, `ctrl+w`, and `ctrl+u` SHALL edit the query. When a query change leaves the active column with no selectable row while the other column has one, the other column SHALL become active.

#### Scenario: First keystroke filters both columns

- **WHEN** the popup is open and the user types a character
- **THEN** both columns narrow to rows matching the query

#### Scenario: Empty active column hands off to the other column

- **WHEN** the Tasks column is active and the query matches no task but matches a Projects row
- **THEN** the Projects column becomes active with its cursor on a matching row

### Requirement: ctrl+g cycles the cursor through needs-input rows

Inside the popup, `ctrl+g` SHALL move the cursor to the next row that needs input, in the order Tasks column top-to-bottom then Projects column top-to-bottom, wrapping around, switching the active column as needed. It SHALL NOT close the popup or navigate away, and SHALL be a no-op when no row needs input.

#### Scenario: ctrl+g walks Tasks then Projects and wraps

- **WHEN** one task in the Tasks column and one role in the Projects column need input, the cursor is on the task's row, and the user presses `ctrl+g` twice
- **THEN** the first press moves the cursor to the Projects role and the second press moves it back to the task, and the popup stays open

#### Scenario: ctrl+g with nothing needing input

- **WHEN** no row in either column needs input and the user presses `ctrl+g`
- **THEN** the cursor does not move and the popup stays open

### Requirement: Jump switcher opens on the first needs-input row, else the current task

When the popup opens, the cursor SHALL land on the first needs-input row in the `ctrl+g` order. If no row needs input, it SHALL land on the currently-viewed task in whichever column contains it. Failing both, it SHALL land on the top of the Tasks column.

#### Scenario: Opens on the first needs-input row

- **WHEN** at least one row needs input and the popup opens
- **THEN** the cursor is on the first needs-input row (Tasks column first) and pressing Enter goes there

#### Scenario: Opens on the current task when nothing needs input

- **WHEN** no row needs input and the user opens the popup while viewing a task
- **THEN** the cursor is on that task's row in its column

### Requirement: Enter jumps to the selection and Esc clears or closes

Enter on a Tasks row SHALL close the popup and open that task's agent view (keeping the Tasks tab cursor in sync) without restarting an exited session. Enter on a Projects row SHALL close the popup, leave the agent view if open, switch to the Projects tab, and land on that role exactly as Enter on the Projects rail does (expanding collapsed ancestors and reattaching as needed); a Projects row with no task of its own SHALL land on the Projects tab with that orchestrator row selected. Esc SHALL clear the query when one is applied, and close the popup (restoring the prior view and focus) when none is. `ctrl+q` SHALL close the popup immediately.

#### Scenario: Enter on a task opens it

- **WHEN** the cursor is on a Tasks row and the user presses Enter
- **THEN** the popup closes and that task's agent view is shown

#### Scenario: Enter on a Projects role from the agent view

- **WHEN** the popup was opened from the classic agent view and the user presses Enter on a Projects role nested under a collapsed coordinator
- **THEN** the agent view is exited, the Projects tab is shown, the coordinator is expanded, and the rail selection and focus land on that role

#### Scenario: Esc clears the filter first

- **WHEN** a filter query is applied and the user presses Esc
- **THEN** the query is cleared, both columns show all rows, and the popup stays open

#### Scenario: Esc closes when no filter is applied

- **WHEN** no filter query is applied and the user presses Esc
- **THEN** the popup closes and the view and focus it was opened from are restored
