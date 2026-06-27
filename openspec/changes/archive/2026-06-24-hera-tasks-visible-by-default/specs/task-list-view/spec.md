# Task List View

## MODIFIED Requirements

### Requirement: Hide hera-managed tasks toggle

The task list SHALL provide a single hera-visibility toggle bound to the `H` key. The toggle SHALL default to OFF (hera-managed tasks **visible** inline), so the Tasks tab shows hera-spawned workers and live coordinators alongside plain tasks by default, each marked with a per-row hera-role indicator. While the toggle is ON, the list SHALL hide every **hera-managed** task and SHALL show freelancer and plain non-hera tasks; pressing `H` toggles between the two states.

A task SHALL be classified as **hera-managed** when EITHER of the following holds:

- It is a hera-spawned worker — its `task_meta` `hera.role` is `worker` (the sidecar stamped at spawn/join, which is permanent and is never cleared when a binding ends); OR
- It holds at least one live hera binding (a binding whose `ended_at` is unset) to a role of kind `coordinator` or `worker`, as reported by the hera bindings/roles store.

A task SHALL be classified as a **freelancer** (and therefore SHALL remain visible regardless of the toggle) when it is neither a hera-spawned worker nor holds a live coordinator/worker binding — i.e. it has no live binding, or holds only `freelance`-kind live bindings. A plain non-hera task (no hera role at all) SHALL likewise always remain visible.

The toggle SHALL compose with the substring filter (`/`) — each is an independent exclusion applied in the same row-build pass. In remote (`--remote`) mode, where no binding-query REST endpoint exists, the live-binding signal MAY fall back to a best-effort union of the `task_meta` `hera.role` worker and coordinator entries; this MAY report a finished worker or coordinator as still managed until the next tick refresh, and is a known degradation documented in the design.

#### Scenario: Hera worker visible by default, hidden by H

- **WHEN** a task is a hera-spawned worker and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a worker indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Live coordinator visible by default, hidden by H

- **WHEN** a task holds a live coordinator-kind binding and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a coordinator indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Freelancer always visible

- **WHEN** a task has no live hera binding (or only `freelance`-kind live bindings) and is not a hera-spawned worker
- **THEN** it is visible regardless of the toggle state

#### Scenario: Plain non-hera task always visible

- **WHEN** a task holds no hera role at all
- **THEN** it is visible regardless of the toggle state

#### Scenario: Toggle composes with the substring filter

- **WHEN** the toggle is ON and a substring filter is active
- **THEN** a task is visible only if it is not hera-managed AND matches every substring term

## ADDED Requirements

### Requirement: Per-task hera-role indicator

Each task row SHALL render a hera-role indicator in a dedicated indicator cell when the task participates in Hera, so a hera-managed task is identifiable at a glance without opening the Hera tab. A task holding a Hera coordinator role SHALL render the coordinator glyph; a hera-spawned worker (or a task holding a live worker-kind binding) that is not a coordinator SHALL render a distinct worker glyph. The coordinator glyph SHALL take precedence if a task would qualify for both. Freelancer and plain non-hera tasks SHALL render no hera-role indicator. The indicator cell SHALL consume row width only when an indicator is present (the name column reclaims the space otherwise) and SHALL be orthogonal to — never replace — the status and PR-review glyphs.

Derived from: `internal/tui/taskview/tasklist.go:1282` (existing coordinator indicator cell), `internal/tui/theme/theme.go:58` (`IconCoordinator`).

#### Scenario: Worker row shows the worker indicator

- **WHEN** a task row is a hera-spawned worker that holds no coordinator role
- **THEN** the row renders the worker glyph in the hera-role indicator cell

#### Scenario: Coordinator outranks worker

- **WHEN** a task qualifies as both a coordinator and a worker
- **THEN** the row renders the coordinator glyph, not the worker glyph

#### Scenario: Plain task renders no hera indicator

- **WHEN** a task holds no hera role
- **THEN** no hera-role indicator cell is drawn and the name column reclaims the space
