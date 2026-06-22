# hera-view delta: rail `/` name filter

## ADDED Requirements

### Requirement: The rail supports a `/` substring name filter

While the RAIL is focused, pressing `/` SHALL enter a rail search INPUT mode. While in input mode, typed characters SHALL build a filter query and the rail SHALL narrow to the rows whose name matches the query by case-insensitive substring, across coordinators, agents (workers and sub-coordinators), and freelancers. Whitespace-separated query terms SHALL each match the row name (AND semantics — every term must be a substring for the row to match); an empty or all-whitespace query SHALL match every row (no narrowing until a real term is typed).

`/` SHALL be RAIL-focus-only: when focus is in a COORD or AGENT pane the `/` rune SHALL be forwarded to the bound task's PTY and MUST NOT enter filter mode (the focus-gating contract).

While a filter is active (the query is non-empty) the rail SHALL remain ancestry-preserving and legible against the nested tree:

- An orchestrator (a root coordinator OR a nested sub-orchestrator) whose name matches, OR which has any descendant role or sub-orchestrator whose name matches, SHALL remain visible so a matching agent always keeps its parent coordinator header. A bridging worker row SHALL remain visible when it bridges a visible sub-orchestrator.
- Collapsed nodes (orchestrators, per-coordinator Archive expandos) and the Freelance / bottom-Archive sections SHALL auto-expand while a filter is active so matching rows are never hidden behind a fold. The persisted fold state MUST be left unchanged and restored when the filter is cleared.
- A section header (`Pinned`, `Freelance (N)`, the bottom `Archive (N)`) or a separator rule SHALL render only when it has at least one visible row beneath it, so the operator never lands on an empty section.

`Esc` while in input mode SHALL exit search and restore the full, unfiltered rail (clearing the query). `Enter` while in input mode SHALL accept the filter — keeping the query applied but leaving input mode so `j`/`k` navigate the filtered set and normal rail key handling resumes. Re-pressing `/` after acceptance SHALL re-enter input mode with the current query preserved for editing.

The active query SHALL be shown unobtrusively: a `/ <query>` input line at the top of the rail while typing, and the active query reflected in the rail border title once accepted (and while typing).

While in input mode the rail's mutation keys (`w`/`r`/`a`/`s`/`S`/`P`/`Ctrl+D` and `Enter`-reattach) SHALL NOT fire, and the global rune shortcuts (`1`/`2`/`3` tab-switch, `q` quit, `?` help) SHALL NOT fire; those keystrokes are filter input instead. After the filter is accepted (input mode off), normal rail and global key handling SHALL resume.

Derived from: `internal/tui/hera/rail.go` (filter state, filter-aware `buildRows`, `/ <query>` line, dynamic title), `internal/tui/hera/page.go` (`handleRailMutation` skip-while-filtering, `RailFiltering()`), `internal/tui/app.go` (global rune-shortcut guard mirroring `a.tasklist.Filtering()`).

#### Scenario: `/` narrows the rail to matching rows

- **WHEN** the operator presses `/` while the rail is focused and types a query
- **THEN** the rail MUST show only rows whose name matches the query (case-insensitive substring, every whitespace-separated term), hiding non-matching coordinators, agents, and freelancers

#### Scenario: A matching nested agent keeps its parent coordinator visible

- **WHEN** a filter matches an agent (or sub-orchestrator) whose name does not match its parent coordinator's name
- **THEN** the parent coordinator header (and any intermediate bridging worker rows) MUST remain visible and expanded so the matching row is shown nested under it

#### Scenario: A collapsed node containing a match auto-expands

- **WHEN** a filter matches a role nested under an orchestrator the operator had collapsed
- **THEN** that orchestrator MUST render expanded while the filter is active, AND its persisted collapsed state MUST be restored unchanged once the filter is cleared

#### Scenario: Empty sections are pruned

- **WHEN** a filter is active and no Freelance (or Archive) member matches
- **THEN** the Freelance (or Archive) section header and its separator rule MUST NOT render

#### Scenario: Esc restores the full rail

- **WHEN** the operator presses `Esc` while in search input mode
- **THEN** the filter MUST clear, input mode MUST exit, and the rail MUST render every row it showed before the filter

#### Scenario: Enter accepts the filter and returns to navigation

- **WHEN** the operator presses `Enter` while in search input mode
- **THEN** input mode MUST exit, the query MUST stay applied (the rail stays filtered), and `j`/`k` MUST navigate the filtered set

#### Scenario: Mutation and global keys are filter input while typing

- **WHEN** the operator is in search input mode and types a character that is otherwise a rail mutation key (`a`, `w`, `P`, …) or a global shortcut (`1`, `2`, `q`, `?`)
- **THEN** that character MUST be appended to the filter query and MUST NOT trigger the mutation, switch tabs, quit, or open help

#### Scenario: `/` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `/`
- **THEN** the `/` rune MUST be forwarded to the bound task's PTY and MUST NOT enter rail filter mode
