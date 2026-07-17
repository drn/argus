# Hera View

## MODIFIED Requirements

### Requirement: PR indicator in the roster (area 6)

The system SHALL mark a roster row's status cell with a trailing `PR` token when that row's task carries a non-empty `url` in the daemon-populated `task_meta` "pr" namespace. The indicator is best-effort and read once per refresh via `ListMetaByNamespace("pr")`; it is never fetched by the view. The `PR` token composes with whatever status text the row already shows (e.g. `ready PR`, `idle PR`, `needs-input PR`) rather than being a mark appended after the agent's name.

Derived from: `internal/tui/hera/details.go` (`hasPR`, `rosterStatusText`), `internal/tui/hera/page.go` (`doRefresh` reads the "pr" namespace).

#### Scenario: PR mark from cached meta

- **WHEN** a roster row's "pr" meta has a non-empty url
- **THEN** its status cell's text carries a trailing `PR` token, composed with whatever status label the row already shows

## ADDED Requirements

### Requirement: Agents roster renders as an aligned, scrollable table (area 6)

The Details pane's `Agents (N):` roster SHALL render as a compact, left-aligned table with four columns — **status** (the existing status icon plus a short text label mirroring `widget.RoleStatusIcon`'s precedence: needs-input / working / ready / failed / done / idle / live / unbound, with a `PR` token composed in per the PR-indicator requirement), **name** (the role name), **archetype** (the role's diligence archetype, `RoleView.Archetype`), and **model** (the resolved LLM model applied to that role, `RoleView.AppliedModel`) — preceded by a `STATUS  NAME  ARCHETYPE  MODEL` column-header row. Archetype and model are read directly from the already-annotated `RoleView` the rail's model already carries (`Archetype` from the role row; `AppliedModel` stamped by `HeraPage`'s `tierResolver` during `doRefresh`, off the Draw path) — no additional daemon, store, or MCP read. A role with no resolved archetype or model (no profile consulted, or the CLI/backend default applied) renders `—` in that cell, never a blank.

Column widths size to the widest cell in each column, capped at a per-column maximum, and shrink — model, then archetype, then name, then status, in that priority order — toward zero when the pane is narrower than the ideal total width. Every cell value is truncated RUNE-safely (never a byte slice mid-codepoint), with a trailing `…` when clipped. A column shrunk to zero width simply stops rendering rather than corrupting the row layout or bleeding into a neighboring pane. `DetailsView.ContentHeight()` includes the column-header row in the roster's row budget whenever the roster is non-empty (an empty roster still renders the single `(none)` line with no header), staying in exact lockstep with `Draw`'s emitted rows.

When the roster has more agents than the row budget its allotted pane height affords (`DetailsView.rosterVisibleRows`, cached from the current Draw's remaining row budget), the system SHALL render a SCROLLABLE window of `rosterVisibleRows` agents starting at `DetailsView.rosterScroll`, rather than silently truncating the tail — every agent SHALL be reachable by scrolling. The window is `[rosterScroll, rosterScroll+rosterVisibleRows)` into the agent list; `rosterScroll` is clamped into `[0, max(0, total-rosterVisibleRows)]` on every Draw (`clampRosterScroll`), so a shrinking pane or a shrinking roster (an agent completing and dropping off) always re-clamps rather than leaving the window stranded past the end of the list. `j`/`k`/`Up`/`Down` in the focused Details region SHALL scroll the roster FIRST (`HeraPage.handleDetailsKey` tries `DetailsView.ScrollRoster` before forwarding to the embedded plan widget) — the SAME physical keys the plan widget already binds for its own stage navigation, layered rather than duplicated: `ScrollRoster` consumes the key only while the roster can still move in the requested direction; once at a bound (or when the roster fits entirely, so it never needed to scroll), the key falls through to the plan widget's navigation unchanged, so the two surfaces never fight over a keystroke. The roster's scroll offset resets to the top on a genuine selection change (a different orchestrator, or none previously selected) but is PRESERVED across a same-orchestrator refresh (the ~1s debounced tick re-selecting the same coordinator), so an operator's mid-read scroll position never snaps back unexpectedly.

Derived from: `internal/tui/hera/details.go` (`computeRosterColumns`, `rosterColStarts`, `rosterTruncate`, `archetypeDisplay`/`modelDisplay`, `rosterStatusText`, `drawRosterHeader`, `drawRosterRow`, `ContentHeight`, `rosterScroll`/`rosterVisibleRows`, `rosterMaxScroll`, `clampRosterScroll`, `ScrollRoster`, `SetOrch`), `internal/tui/hera/model.go` (`RoleView.Archetype`/`AppliedModel`), `internal/tui/hera/page.go` (`SetTierResolver`, `doRefresh`, `handleDetailsKey`, `rosterScrollDelta`).

#### Scenario: Roster shows the column header and per-agent archetype/model

- **WHEN** a coordinator with at least one worker role is selected
- **THEN** the Details pane's roster shows a `STATUS  NAME  ARCHETYPE  MODEL` header row followed by one row per agent, each showing its resolved archetype and model

#### Scenario: Unresolved archetype/model renders an em-dash placeholder

- **WHEN** a worker role carries no diligence archetype or no resolved model (fail-open / no profile consulted)
- **THEN** its ARCHETYPE and/or MODEL cell renders `—`, not a blank cell

#### Scenario: Status icon and label never disagree with the rail

- **WHEN** a roster row's status cell renders its text label
- **THEN** the label is derived from the SAME precedence (`widget.RoleStatusIcon`'s inputs) that chose the row's status icon, so the two never contradict each other

#### Scenario: Narrow pane shrinks columns instead of corrupting the layout

- **WHEN** the Details pane is narrower than the roster's ideal total column width
- **THEN** columns shrink — model first, then archetype, then name, then status — toward zero width, truncating cell values rune-safely, without panicking or misaligning the table

#### Scenario: ContentHeight includes the header row

- **WHEN** the Details region sizes the roster panel via `ContentHeight()` and the roster is non-empty
- **THEN** the returned height accounts for the column-header row in addition to one row per agent, matching exactly what `Draw` emits

#### Scenario: A long roster scrolls instead of truncating the tail

- **WHEN** a coordinator's roster has more agents than the roster panel's row budget affords
- **THEN** only a window of agents is drawn, and scrolling down with `j`/`Down` brings later agents — including the last one — into view; every agent is reachable

#### Scenario: Roster scroll clamps at both ends

- **WHEN** the roster is already showing its first agent (scrolled fully up) or its last agent (scrolled fully down)
- **THEN** a further scroll-up (respectively scroll-down) key is a no-op — the offset never goes negative or past the point where the last agent is visible

#### Scenario: Roster scroll takes priority, then falls through to the plan widget

- **WHEN** the focused Details region receives `j`/`k`/`Up`/`Down` and the roster still has room to scroll in that direction
- **THEN** the roster scrolls and the embedded plan widget's stage cursor does NOT move; once the roster can't scroll further in that direction, the SAME key reaches the plan widget's own navigation instead

#### Scenario: Roster scroll survives a same-orchestrator refresh but resets on a real selection change

- **WHEN** the operator has scrolled a coordinator's roster and a refresh tick re-selects the SAME orchestrator
- **THEN** the scroll position is unchanged; selecting a DIFFERENT orchestrator (or no orchestrator) resets the scroll position to the top
