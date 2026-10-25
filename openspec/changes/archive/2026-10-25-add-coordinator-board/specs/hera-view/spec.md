## MODIFIED Requirements

### Requirement: Details region stacks roster over the plan DAG (area 6)

For a coordinator selection the system SHALL render the read-only `" Details "` roster (top) and either the embedded plan graph or the coordinator's human board (bottom). The roster is sized to its natural content height, capped at half the region (so the lower panel keeps at least half), clamped to a minimum of 3 rows and to the region height; the lower panel fills the remainder and is skipped when fewer than 2 rows remain. With `ui.coordinator_board` enabled, unread items at coordinator selection time select the board; otherwise the plan is selected. Same-selection refreshes SHALL preserve the chosen lower panel. The `p` action SHALL toggle plan/board when the rail or details region is focused, yielding to active rail-filter typing. With the setting disabled and the plan visible, its existing `" Plan "` title SHALL remain unchanged; otherwise the header SHALL show plan/board navigation and the unread count.

The selected lower panel SHALL own details-region navigation: the plan keeps its four-way navigation, `Enter`/`Space`, and `Esc`, while the board uses the coordinator-board capability's item navigation, expansion, reply and dismissal actions. Coordinator-region clicks SHALL keep keyboard focus on the Hera page wrapper; hidden plan content SHALL NOT receive clicks. The existing Cmd+Right focus ladder SHALL reach the details/board region.

When the plan is visible and the user presses Enter on a plain leaf node whose role is buried under a COLLAPSED ancestor coordinator in the rail, the system SHALL first expand the rail — uncollapsing the target role's containing orchestrator and every ancestor on its canonical parent chain to the root (the same fold-independent parentage the rail nests by, handling deeply nested sub-coordinators) — so the role's row is built, and THEN perform the join. Fold state is resolved from the full model, never from the currently-built rows, so a folded coordinator never swallows the join. The expand persists like a user collapse-toggle.

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected, the plan is the chosen lower panel, and the region is tall enough
- **THEN** the `" Details "` roster and plan graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Unread board at selection time

- **WHEN** a coordinator with unread board items is selected with the setting enabled
- **THEN** the roster renders above the board, and later refreshes preserve that choice until an explicit toggle or a different coordinator selection

#### Scenario: Enter on a plain leaf node jumps to its role within the Hera view

- **WHEN** the plan is visible, the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the system selects that node's role in the Hera rail (by its bound task id) and moves focus to that role's agent pane, staying on the Hera tab (it SHALL NOT switch to the Tasks view)

#### Scenario: Enter on a leaf node whose session is dead restarts and joins it

- **WHEN** the plan is visible and the user presses Enter on a plain leaf node whose backing agent session has exited (no live session in the runner)
- **THEN** the system restarts-and-joins that session, identically to the rail's Enter — firing the same reattach under the same liveness gate (a dead session of any role, or a live non-coordinator role, fires it; a live coordinator stays navigate-only) — so the node never merely selects without restarting

#### Scenario: Enter on a leaf under a collapsed coordinator expands the rail then joins

- **WHEN** the plan is visible and the user presses Enter on a plain leaf node whose containing coordinator (or any ancestor coordinator) is collapsed in the rail, so the role's row is not currently built
- **THEN** the system uncollapses the target's entire ancestor coordinator chain (rebuilding and persisting the rail like a user expand), then selects that node's role and moves focus to its agent pane — the join lands regardless of fold state instead of silently doing nothing
