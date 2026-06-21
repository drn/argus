## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `ready_to_close` wins over everything with a distinct review glyph; otherwise (2) the hera role status when present — working, blocked, done, failed, or idle each map to a distinct glyph/style, with `failed` rendering a red ✕; otherwise (3) binding presence (`Live`) renders a "live" glyph; otherwise (4) an unbound/dimmed glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables. The status vocabulary (idle/working/blocked/done/failed) is shared 1:1 with the role-status enum so the rail, the plan view, and the gater never drift.

Derived from: `internal/tui/hera/rail.go:514` (`statusIcon`), `internal/tui/hera/model.go:214` (`buildRoleView` reads `ready_to_close`).

#### Scenario: ready_to_close overrides status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true` AND the role status is working
- **THEN** the row renders the review/ready glyph, not the working glyph

#### Scenario: Hera role status drives the glyph

- **WHEN** a role has a status row of `blocked` and is not ready_to_close
- **THEN** the row renders the needs-input/blocked glyph

#### Scenario: failed renders the red cross glyph

- **WHEN** a role has a status row of `failed` and is not ready_to_close
- **THEN** the row renders the red ✕ failed glyph distinct from the done glyph

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

## ADDED Requirements

### Requirement: Cancelled planned nodes render distinctly in the plan view

The plan view SHALL render a cancelled planned node (a role carrying the `cancelled_at` marker) as a distinct grey ✕ node that remains visible in the DAG, rather than dropping it. The node keeps its position and edges so the coordinator can see what was reconciled out of the plan; it is visually distinguished from a live, planned (violet `○`), done (green `✓`), or failed (red `✕`) node.

#### Scenario: A cancelled node stays visible and distinct

- **WHEN** the plan view renders an orchestrator containing a cancelled planned node
- **THEN** the node is drawn as a distinct grey cancelled glyph in its DAG position, not omitted

#### Scenario: Cancelled is distinct from failed

- **WHEN** the plan view renders a cancelled node and a failed node
- **THEN** the two use distinct glyphs/styles (grey cancelled vs red failed)
