## ADDED Requirements

### Requirement: ctrl+g jumps to the next role needing input in rail order, cycling on repeat

The system SHALL provide a dedicated, popup-free jump directly to the next role needing input — independent of the unified task/role switcher (`ctrl+j`) — reachable from the classic fullscreen agent view, the plain Tasks tab, and every Hera focus region (rail, coordinator pane, agent pane). The candidate ring is today's built rail order (Pinned → Active depth-first → Freelance → Archive), including any row the rail's partial-fold reveal already surfaces behind a closed ancestor fold. A candidate row's OWN needs-input signal qualifies it (the same signal the rail's leaf "(?)" glyph and the switcher's needs-input-first sort both key on) — a coordinator header's rolled-up subtree signal does NOT independently qualify the header, since the descendant that actually needs input is its own, separate candidate. The scan starts strictly after the current cursor position and wraps around the whole ring back to (and including) the cursor itself, so repeated presses cycle forward through every candidate in turn without repeatedly landing on the one just visited, and a single remaining candidate keeps re-selecting itself once the ring wraps all the way around. Landing on a candidate reuses the existing ancestor-expand + reattach-a-dead/suspended-session + focus sequence (`JumpToTask`) the switcher's own Hera landing already uses — no new selection/expansion/focus logic.

Derived from: `internal/tui/hera/rail.go` (`railRow.needsInputTaskID`, `Rail.NextNeedsInputTaskID`), `internal/tui/hera/page.go` (`HeraPage.JumpToNextNeedsInput`), `internal/tui/app.go` (`jumpToNextNeedsInput`).

#### Scenario: Jumps to the sole role needing input

- **WHEN** exactly one role needs input and the user presses `ctrl+g` from anywhere
- **THEN** the Hera tab becomes active and the rail selection lands on that role, with its ancestors expanded if it was folded away

#### Scenario: Repeated presses cycle through every candidate without repeating

- **WHEN** two or more roles need input and the user presses `ctrl+g` repeatedly
- **THEN** each press advances to the next candidate in rail order, never re-selecting the one the cursor already sits on, until the ring wraps back to the first candidate visited

#### Scenario: Reachable from a focused Hera pane without leaking to the PTY

- **WHEN** a Hera coordinator or worker terminal pane holds focus and the user presses `ctrl+g`
- **THEN** the jump fires and the byte never reaches the pane's live PTY

#### Scenario: A closed ancestor's needs-input descendant is still reachable

- **WHEN** a role needing input is nested under a collapsed coordinator (already peeking through via the rail's partial-fold reveal)
- **THEN** `ctrl+g` still finds it, fully expands its ancestor chain, and lands the selection there

#### Scenario: No role needs input is a safe no-op

- **WHEN** the user presses `ctrl+g` while no role currently needs input
- **THEN** a transient notice is shown and nothing else changes (no crash, no unexpected navigation)

### Requirement: A top-level coordinator's own needs-input signal is not a ctrl+g jump target

The system SHALL exclude a top-level orchestrator's coordinator role from `ctrl+g`'s candidate ring even when that coordinator's own needs-input signal is set, because the coordinator role is folded entirely into the orchestrator's header row (see "Coordinator folds into the orchestrator header") and is never emitted as a role-bearing row `SelectByTaskID` could land a jump on — offering it as a candidate would produce a found-but-unreachable dead cycle stop. A nested sub-coordinator, which bridges as an ordinary role-bearing worker row in its parent orchestrator, is unaffected and remains a reachable candidate. The coordinator's own need stays visible via the header's existing rolled-up "(?)" glyph; only the direct-jump reachability is excluded.

#### Scenario: A top-level coordinator's own need is invisible to the cycle

- **WHEN** a top-level orchestrator's coordinator role itself needs input and no other role does
- **THEN** `ctrl+g` reports no role needs input, even though the header still shows the rolled-up "(?)" glyph

#### Scenario: A nested sub-coordinator's own need remains a reachable candidate

- **WHEN** a nested sub-coordinator (bridged as a worker row in its parent orchestrator) itself needs input
- **THEN** `ctrl+g` finds it and lands the jump there like any other role-bearing row
