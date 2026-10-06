## ADDED Requirements

### Requirement: Global sandbox extra-write paths are editable

The Sandbox category SHALL list each global `sandbox.extra_write` path as a row and let the user add (`n`), edit (`e` or Enter), and delete (`d`) paths inline. Enter SHALL persist the list to `sandbox.extra_write`; Escape SHALL abort. Blank paths and paths containing a comma SHALL be rejected without persisting.

#### Scenario: Adding a path

- **WHEN** the user presses `n` in the Sandbox category, types `~/Downloads`, and presses Enter
- **THEN** `sandbox.extra_write` contains `~/Downloads` and a row for it appears

#### Scenario: Deleting a path

- **WHEN** the user selects a path row and presses `d`
- **THEN** the path is removed from `sandbox.extra_write`
