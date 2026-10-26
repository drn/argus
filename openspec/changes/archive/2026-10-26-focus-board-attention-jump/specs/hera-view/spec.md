## MODIFIED Requirements

### Requirement: ctrl+g jumps to the next role needing input in rail order, cycling on repeat

The system SHALL provide a dedicated, popup-free jump directly to the next role needing input — independent of the unified task/role switcher (`ctrl+j`) — reachable from the classic fullscreen agent view, the plain Tasks tab, and every Hera focus region (rail, coordinator pane, agent pane). The candidate ring is today's built rail order (Pinned → Active depth-first → Freelance → Archive), including any row the rail's partial-fold reveal already surfaces behind a closed ancestor fold. A candidate row's OWN needs-input signal qualifies it (the same signal the rail's leaf "(?)" glyph and the switcher's needs-input-first sort both key on) — a coordinator header's ROLLED-UP subtree signal (`SubtreeNeedsInput`) does NOT independently qualify the header, since a descendant that actually needs input is its own, separate candidate; a coordinator header's OWN needs-input signal (`CoordRole().needsInputOwn()` — the coordinator's own live session or `blocked` status, as opposed to any descendant's rollup) DOES independently qualify the header, whether the orchestrator is a top-level root or a coordinator-spawned nested sub-team sharing its coordinator agent with a parent orchestrator — both render through the identical header-only row, and neither is a role-bearing row, so both are reached through the header rather than a role match. The scan starts strictly after the current cursor position and wraps around the whole ring back to (and including) the cursor itself, so repeated presses cycle forward through every candidate in turn without repeatedly landing on the one just visited, and a single remaining candidate keeps re-selecting itself once the ring wraps all the way around. Landing on a needs-input-only candidate reuses the existing ancestor-expand + reattach-a-dead/suspended-session + focus sequence (`JumpToTask`) the switcher's own Hera landing already uses — no new selection/expansion/focus logic; landing on a coordinator header resolves through the same `SelectByTaskID` primitive, extended to match a header row via its coordinator's own task id, and focus lands in the coordinator pane exactly as manual cursor navigation onto that header already does today.

When a coordinator-spawned sub-team's parent and child orchestrators share the SAME underlying coordinator task (one coordinator agent driving both), and that task needs input, both header rows independently qualify as candidates; landing resolves to whichever one `SelectByTaskID` matches first in rail row order — the same first-match convention already governing any other multi-binding task reachable through two role rows. This is a known, accepted characteristic of the existing multi-binding model, not a defect this requirement guards against.

This is the "count>=1" half of a larger "problem-child excursion" state machine (see the new Requirement below): the FIRST time the whole-rail needs-input count transitions from zero to one or more, the rail captures a snapshot of its fold/selection state before doing anything else. `ctrl+g`, before jumping, ensures that snapshot is armed (belt-and-suspenders — it is normally already armed by the transition itself). When the count is instead zero — nothing left to jump to — `ctrl+g` no longer just flashes a no-op notice: if an excursion snapshot is held (from an earlier interruption that has since been fully resolved but never explicitly discharged), it RESTORES that snapshot's fold/selection state instead, discards it, and flashes "Rail restored"; only when NO snapshot is held does it fall back to the plain no-op notice. A restore never switches tabs or tears down a live fullscreen agent view — it is a background rail-state fix, not something that needs to be watched happen.

Derived from: `internal/tui/hera/rail.go` (`railRow.needsInputTaskID`, `Rail.NextNeedsInputTaskID`, `Rail.SelectByTaskID`, `Rail.NeedsInputCount`, `Rail.RestoreExcursion`, `Rail.noteExcursionTransition`), `internal/tui/hera/model.go` (`Model.NeedsInputTotalCount`), `internal/tui/hera/page.go` (`HeraPage.JumpToNextNeedsInput`), `internal/tui/app.go` (`jumpToNextNeedsInput`).

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

#### Scenario: No role needs input and no excursion is held is a safe no-op

- **WHEN** the user presses `ctrl+g` while no role currently needs input and no excursion snapshot is held
- **THEN** a transient "No role needs input" notice is shown and nothing else changes (no crash, no unexpected navigation)

#### Scenario: No role needs input but an excursion is held restores the rail

- **WHEN** the user presses `ctrl+g` while no role currently needs input, and an excursion snapshot is held from an earlier interruption that has since resolved
- **THEN** the rail's fold/selection state is restored to the snapshot, the snapshot is discarded, a "Rail restored" notice is shown, and neither the active tab nor the classic agent view is disturbed

#### Scenario: A top-level coordinator's own need is a reachable candidate

- **WHEN** a top-level orchestrator's coordinator role itself needs input and no other role does
- **THEN** `ctrl+g` finds it and lands the selection on the orchestrator's header row, moving focus to the coordinator pane — the header shows its own "(?)" glyph, exactly as manual cursor navigation onto that header already would

#### Scenario: A coordinator-spawned nested sub-team's own need is reachable

- **WHEN** a coordinator-spawned nested sub-team's coordinator role itself needs input (the parent orchestrator's coordinator agent also drives this child orchestrator) and no other role does
- **THEN** `ctrl+g` finds it (via the child's own signal) and expands any collapsed ancestor; because the parent and child headers share the identical underlying coordinator task, landing resolves to whichever of the two `SelectByTaskID` matches first in rail row order — structurally always the parent header, since a header row is placed before any of its nested children's rows — moving focus to the coordinator pane for that (shared) task. This is the same first-match convention the next scenario documents, not a distinct landing rule.

#### Scenario: A nested sub-coordinator bridging as a worker row remains reachable

- **WHEN** a nested sub-coordinator that bridges as an ordinary worker row in its parent orchestrator (not a coordinator-spawned header) itself needs input
- **THEN** `ctrl+g` finds it and lands the jump there like any other role-bearing row

#### Scenario: A shared coordinator task's duplicate header candidates resolve to the first match

- **WHEN** a coordinator-spawned sub-team's parent and child headers both qualify because they share the same underlying coordinator task, and that task needs input
- **THEN** `ctrl+g` lands on whichever of the two headers `SelectByTaskID` matches first in rail row order, consistently — matching the existing multi-binding convention rather than alternating between them

When `ui.coordinator_board` is enabled, the candidate ring SHALL additionally include every coordinator with unread board items, including top-level headers and nested sub-coordinator bridging rows. A coordinator qualifying through both signals SHALL appear once. Board targets SHALL land on that coordinator's exact row, even when its task is also bound in another orchestrator, and run the existing selection-time board auto-landing behavior and reattach semantics. After landing, the Board SHALL hold keyboard focus in the agent/details region, with details mode and the Board visible. The cursor SHALL select the first unread item in displayed order and follow it into view. Landing SHALL NOT expand or mark any item read; expansion by Enter retains the existing read acknowledgment contract. Ordinary rail selection and refresh SHALL continue preserving the selected plan view. A repeated jump to the already-selected unread coordinator SHALL also show its board. Folded paths SHALL reveal board candidates through the existing partial-fold mechanism and expand ancestors on landing. The Ctrl+J popup's next-needs-input scan SHALL use the same qualification, preserving its existing all-kanban-group traversal. With the setting disabled, both scans SHALL retain their previous needs-input-only behavior.

#### Scenario: Board-only top-level coordinator

- **WHEN** a top-level coordinator has unread board items but no needs-input signal and the user presses Ctrl+G
- **THEN** the coordinator header is selected and its board is shown

#### Scenario: Board-only nested coordinator behind a fold

- **WHEN** a nested sub-coordinator has unread board items under a collapsed ancestor
- **THEN** its bridging row is revealed, Ctrl+G expands its ancestor path and selects that coordinator with its board shown

#### Scenario: Mixed signals keep rail order without duplicate stops

- **WHEN** workers need input and coordinators have unread boards, including a coordinator also needing input
- **THEN** repeated Ctrl+G presses cycle in built rail order with one stop per coordinator, and Ctrl+J's next-needs-input scan includes the same board candidates

#### Scenario: Board setting disabled

- **WHEN** ui.coordinator_board is off and the only attention signal is an unread board
- **THEN** neither jump scan qualifies that coordinator and Ctrl+G retains its existing restore or no-op behavior

#### Scenario: Board landing focuses the first unread item without reading it

- **WHEN** Ctrl+G lands on a coordinator whose Board contains read items before multiple unread items
- **THEN** the Board has keyboard focus (FocusAgent with details mode and Board shown), the cursor selects the first unread item, and cursor following scrolls it into view
- **AND** no item is expanded or marked read just by landing

#### Scenario: Repeated board landing resets the cursor without changing ordinary refresh

- **WHEN** the already-selected coordinator is the sole unread Board candidate and the operator has switched to the plan or moved the Board cursor
- **THEN** Ctrl+G reopens and focuses the Board on its first unread item without expanding or acknowledging it
- **AND** ordinary rail selection and refresh still preserve the selected plan

#### Scenario: Needs-input-only landings retain their pane focus

- **WHEN** Ctrl+G lands on a worker needing input or a coordinator needing input without an eligible unread Board
- **THEN** the existing landing focuses the worker agent pane or coordinator terminal pane respectively and preserves reattach behavior

