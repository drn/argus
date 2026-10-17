## MODIFIED Requirements

### Requirement: Fresh spawns use recent TUI pane dimensions

Every CreateAndStart caller SHALL fill omitted PTY dimensions from a valid recently reported local TUI size, including headless tasks, Hera spawn/materialization, REST creation and schedule run-now. Explicit dimensions SHALL take precedence individually. Hera roles SHALL use conservative split role dimensions derived from current screen geometry regardless of active tab or fullscreen state, and SHALL NOT inherit Active geometry when their role-specific size is absent or invalid. Other callers SHALL use Active geometry. Reports SHALL expire after two minutes and valid preferences SHALL have at least five rows and twenty columns. Missing, invalid or expired preferences SHALL preserve the existing 24-row, 80-column defaults.

#### Scenario: Fresh coordinator is shown on Projects

- **WHEN** the TUI reports a 98-column split coordinator pane and a fresh coordinator is created without explicit dimensions
- **THEN** its session SHALL start at 98 columns

#### Scenario: No recent TUI report

- **WHEN** no valid report exists or the report is at least two minutes old
- **THEN** omitted dimensions SHALL use the existing 24×80 defaults

#### Scenario: Explicit dimensions

- **WHEN** the caller supplies either PTY dimension explicitly
- **THEN** the report SHALL fill only the omitted dimension

#### Scenario: Report has no role-specific pane

- **WHEN** a report contains only Active geometry and a Hera role is created
- **THEN** its omitted dimensions SHALL use 24×80 defaults rather than Active

#### Scenario: Tasks or agent view is active

- **WHEN** a local TUI publishes a layout while Tasks or an agent view is active
- **THEN** coordinator and worker preferences SHALL still describe the screen's Hera split panes

#### Scenario: Hera fullscreen is active

- **WHEN** a Hera pane is fullscreen
- **THEN** fresh role preferences SHALL remain the narrower split geometry

#### Scenario: REST create omits dimensions

- **WHEN** web, macOS or remote TUI creates a task through REST without dimensions
- **THEN** fresh creation SHALL use the daemon host's recent Active preference or the existing defaults when unavailable

## ADDED Requirements

### Requirement: Redundant attach clears stale repair status

A redundant attach decision SHALL clear existing repair status for that task across main and Hera panes without changing its unresolved width anchor.

#### Scenario: Hidden main pane retains a deferred indicator

- **WHEN** a Hera rebind resolves repair through the redundant-attach gate
- **THEN** the hidden main pane's repair indicator SHALL clear
