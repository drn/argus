## ADDED Requirements

### Requirement: Per-task sandbox override column

The schema SHALL persist a task's sandbox override as a `tasks.sandbox_override`
column with three encoded states: empty string (no override), `"enabled"`, and
`"disabled"`. It SHALL default to empty for existing rows and require no data
migration.

#### Scenario: Sandbox override round-trips

- **WHEN** a task is created with a sandbox override of `"enabled"` or `"disabled"`
- **THEN** the read-back task carries that same value

#### Scenario: Existing rows default to empty

- **WHEN** the new column is added to a database with existing rows
- **THEN** those rows read an empty sandbox override without error
