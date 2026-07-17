# Hera View

## MODIFIED Requirements

### Requirement: The rail supports a `/` substring name filter

While the RAIL is focused, pressing `/` SHALL enter a rail search INPUT mode. While in input mode, typed characters SHALL build a filter query and the rail SHALL narrow to the rows whose name matches the query by case-insensitive substring, across coordinators, agents (workers and sub-coordinators), and freelancers. Whitespace-separated query terms SHALL each match the row name (AND semantics — every term must be a substring for the row to match); an empty or all-whitespace query SHALL match every row (no narrowing until a real term is typed).

`/` SHALL be RAIL-focus-only: when focus is in a COORD or AGENT pane the `/` rune SHALL be forwarded to the bound task's PTY and MUST NOT enter filter mode (the focus-gating contract).

While a filter is active (the query is non-empty) the rail SHALL remain ancestry-preserving and legible against the nested tree:

- An orchestrator (a root coordinator OR a nested sub-orchestrator) whose name matches, OR which has any descendant role or sub-orchestrator whose name matches, SHALL remain visible so a matching agent always keeps its parent coordinator header. A bridging worker row SHALL remain visible when it bridges a visible sub-orchestrator.
- Collapsed nodes (orchestrators, per-coordinator Archive expandos) and the Freelance / bottom-Archive sections SHALL auto-expand while a filter is active so matching rows are never hidden behind a fold. The persisted fold state MUST be left unchanged and restored when the filter is cleared.
- A section header (`Pinned`, `Freelance (N)`, the bottom `Archive (N)`) or a separator rule SHALL render only when it has at least one visible row beneath it, so the operator never lands on an empty section.
- A coordinator/orchestrator heading row (an orchestrator header, or a worker-bridge/pinned-breadcrumb row standing in for a nested coordinator) that is visible ONLY because a descendant matches — its own name, or its folded-in coordinator's name, does NOT itself match the query — is an ANCESTRY-ONLY heading: it SHALL NOT be a valid cursor target (arrow navigation and first-match auto-select skip it entirely) and SHALL render visually dimmed so it is obvious it cannot be selected. A heading whose own name (or folded-in coordinator's name) DOES match the query remains a normal, selectable, non-dimmed row.

The FIRST real match (a selectable row that is not an ancestry-only heading, not a structural fold header) in the narrowed rows SHALL auto-select live on every query change (typing or backspacing), so the operator sees the top candidate highlighted without needing to navigate to it. Up/Down SHALL move the cursor within the narrowed set while remaining in search input mode (so typing/backspacing continues to work), landing only on rows that are themselves a match.

`Esc` while in input mode SHALL exit search and restore the full, unfiltered rail (clearing the query). `Enter` while in input mode SHALL resolve against the CURRENTLY selected row (the auto-selected first match, or wherever Up/Down moved it) — reattaching/entering that row's pane exactly as a normal (non-filtering) Enter would — and SHALL THEN fully clear the filter (query reset, input mode off, full unfiltered rail restored) in the SAME keystroke. There is no intermediate "accepted but still narrowed" resting state: Enter always both selects and clears, never merely one or the other.

The active query SHALL be shown unobtrusively: a `/ <query>` input line at the top of the rail while typing, and the active query reflected in the rail border title while typing.

While in input mode the rail's mutation keys (`w`/`r`/`a`/`s`/`S`/`P`/`Ctrl+D`) SHALL NOT fire, and the global rune shortcuts (`1`/`2`/`3` tab-switch, `q` quit, `?` help) SHALL NOT fire; those keystrokes are filter input instead. `Enter` is the one exception — it is intercepted and handled as select-and-clear (above) rather than falling through as filter input.

Derived from: `internal/tui/hera/rail.go` (filter state, filter-aware `buildRows`, ancestry-only heading detection, first-match auto-select, `/ <query>` line, dynamic title), `internal/tui/hera/page.go` (`handleRailMutation`'s Enter-while-filtering branch: select then `Rail.ClearFilter()`), `internal/tui/app.go` (global rune-shortcut guard mirroring `a.tasklist.Filtering()`).

This Hera-rail filter is intentionally Hera-rail-scoped: the Tasks-tab (`internal/tui/taskview`) `/` filter keeps its own independent two-step (type → Enter locks → navigate → Enter selects) convention and is NOT changed by this requirement.

#### Scenario: `/` narrows the rail to matching rows

- **WHEN** the operator presses `/` while the rail is focused and types a query
- **THEN** the rail MUST show only rows whose name matches the query (case-insensitive substring, every whitespace-separated term), hiding non-matching coordinators, agents, and freelancers

#### Scenario: A matching nested agent keeps its parent coordinator visible

- **WHEN** a filter matches an agent (or sub-orchestrator) whose name does not match its parent coordinator's name
- **THEN** the parent coordinator header (and any intermediate bridging worker rows) MUST remain visible and expanded so the matching row is shown nested under it, rendered as a dimmed, non-selectable ancestry-only heading

#### Scenario: A collapsed node containing a match auto-expands

- **WHEN** a filter matches a role nested under an orchestrator the operator had collapsed
- **THEN** that orchestrator MUST render expanded while the filter is active, AND its persisted collapsed state MUST be restored unchanged once the filter is cleared

#### Scenario: Empty sections are pruned

- **WHEN** a filter is active and no Freelance (or Archive) member matches
- **THEN** the Freelance (or Archive) section header and its separator rule MUST NOT render

#### Scenario: The first real match auto-selects while typing

- **WHEN** the operator types (or backspaces) a query that narrows the rail
- **THEN** the cursor MUST move onto the first real match in the narrowed rows (never an ancestry-only heading or a structural fold header) without any further keypress

#### Scenario: An ancestry-only coordinator heading is skipped by navigation and auto-select

- **WHEN** a filter matches only a descendant of a coordinator/orchestrator heading, so the heading itself is shown purely for ancestry context
- **THEN** that heading row MUST render dimmed, MUST NOT be reachable by Up/Down arrow navigation, and MUST NOT be chosen by first-match auto-select

#### Scenario: A coordinator heading whose own (or folded-in coordinator's) name matches is a real, selectable match

- **WHEN** the query matches an orchestrator's own name, or the name of its folded-in coordinator role
- **THEN** that orchestrator's header row MUST be treated as a real match — selectable, not dimmed, and eligible for first-match auto-select

#### Scenario: Enter selects the current match, jumps into it, and clears the filter in one step

- **WHEN** the operator presses `Enter` while in search input mode, with the cursor resting on a real match (auto-selected or arrow-navigated)
- **THEN** the rail MUST reattach/enter that row's pane exactly as a normal Enter would, AND the filter MUST fully clear (query reset, input mode off) in the SAME keystroke — no second Enter is required

#### Scenario: Esc restores the full rail

- **WHEN** the operator presses `Esc` while in search input mode
- **THEN** the filter MUST clear, input mode MUST exit, and the rail MUST render every row it showed before the filter

#### Scenario: Mutation and global keys are filter input while typing

- **WHEN** the operator is in search input mode and types a character that is otherwise a rail mutation key (`a`, `w`, `P`, …) or a global shortcut (`1`, `2`, `q`, `?`)
- **THEN** that character MUST be appended to the filter query and MUST NOT trigger the mutation, switch tabs, quit, or open help

#### Scenario: `/` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `/`
- **THEN** the `/` rune MUST be forwarded to the bound task's PTY and MUST NOT enter rail filter mode
