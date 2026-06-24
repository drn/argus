## MODIFIED Requirements

### Requirement: Details region stacks roster over the plan DAG (area 6)

For a coordinator selection the system SHALL render BOTH the read-only `" Details "` roster (top) and the embedded `" Plan "` graph (bottom) at once with no toggle. The roster is sized to its natural content height, capped at half the region (so the graph keeps at least half), clamped to a minimum of 3 rows and to the region height; the graph fills the remainder and is skipped when fewer than 2 rows remain. The graph (the plan view) is the ONLY interactive surface — every key in the details region forwards to it (the 4-way navigation, `Enter`/`Space`, and `Esc`), and coordinator-region clicks route to it.

When the user presses Enter on a plain leaf node whose role is buried under a COLLAPSED ancestor coordinator in the rail, the system SHALL first expand the rail — uncollapsing the target role's containing orchestrator and every ancestor on its canonical parent chain to the root (the same fold-independent parentage the rail nests by, handling deeply nested sub-coordinators) — so the role's row is built, and THEN perform the join. Fold state is resolved from the full model, never from the currently-built rows, so a folded coordinator never swallows the join. The expand persists like a user collapse-toggle.

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Plan "` graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Enter on a plain leaf node jumps to its role within the Hera view

- **WHEN** the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the system selects that node's role in the Hera rail (by its bound task id) and moves focus to that role's agent pane, staying on the Hera tab (it SHALL NOT switch to the Tasks view)

#### Scenario: Enter on a leaf node whose session is dead restarts and joins it

- **WHEN** the user presses Enter on a plain leaf node whose backing agent session has exited (no live session in the runner)
- **THEN** the system restarts-and-joins that session, identically to the rail's Enter — firing the same reattach under the same liveness gate (a dead session of any role, or a live non-coordinator role, fires it; a live coordinator stays navigate-only) — so the node never merely selects without restarting

#### Scenario: Enter on a leaf under a collapsed coordinator expands the rail then joins

- **WHEN** the user presses Enter on a plain leaf node whose containing coordinator (or any ancestor coordinator) is collapsed in the rail, so the role's row is not currently built
- **THEN** the system uncollapses the target's entire ancestor coordinator chain (rebuilding and persisting the rail like a user expand), then selects that node's role and moves focus to its agent pane — the join lands regardless of fold state instead of silently doing nothing
