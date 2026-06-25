## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `ready_to_close` wins over everything with a distinct review glyph; otherwise (2) an operator/agent-set `blocked`, `done`, or `failed` hera role status renders its distinct static glyph (`failed` rendering a red ✕, distinct from `done`); otherwise (3) GENUINE activity (`RoleView.IsActive` — a live binding whose bound argus task is `in_progress`) renders the ACTIVE SPINNER's animated frame (see "Active agents animate a spinner glyph"); otherwise (4) an `idle` hera role status renders the static idle glyph; otherwise (5) binding presence (`Live`) renders a "live" glyph; otherwise (6) an unbound/dimmed glyph. The spinner is sourced from REAL session activity, never the stale `working` hera role status (BUG-003): a `working` role that is not genuinely active falls through to (5)/(6) and renders a static glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables. The status vocabulary (idle/working/blocked/done/failed) is shared 1:1 with the role-status enum so the rail, the plan view, and the gater never drift.

Derived from: `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `buildRoleView` reads `ready_to_close`).

#### Scenario: ready_to_close overrides status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true` AND the role status is working
- **THEN** the row renders the review/ready glyph, not the working spinner

#### Scenario: Genuine activity renders the animated spinner

- **WHEN** a role holds a live binding whose bound argus task is `in_progress` and is not blocked/done/failed/ready_to_close
- **THEN** the row renders the active spinner's frame (animated), not a static glyph

#### Scenario: Stale working role-status does not animate

- **WHEN** a role's hera status is `working` but it is not genuinely active (no live binding, or its bound task is no longer `in_progress`)
- **THEN** the row renders a static glyph (live or dimmed-unbound), not the spinner

#### Scenario: Blocked outranks activity

- **WHEN** a role has a status row of `blocked` and is not ready_to_close (even while its bound task is still `in_progress`)
- **THEN** the row renders the needs-input/blocked glyph (static), not the spinner

#### Scenario: failed renders the red cross glyph

- **WHEN** a role has a status row of `failed` and is not ready_to_close (even while its bound task is still `in_progress`)
- **THEN** the row renders the red ✕ failed glyph (static), distinct from the done glyph, not the spinner

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
