## MODIFIED Requirements

### Requirement: ctrl+g jumps to the next role needing input in rail order, cycling on repeat

The system SHALL provide a dedicated, popup-free jump directly to the next role needing input — independent of the unified task/role switcher (`ctrl+j`) — reachable from the classic fullscreen agent view, the plain Tasks tab, and every Hera focus region (rail, coordinator pane, agent pane). The candidate ring is today's built rail order (Pinned → Active depth-first → Freelance → Archive), including any row the rail's partial-fold reveal already surfaces behind a closed ancestor fold. A candidate row's OWN needs-input signal qualifies it (the same signal the rail's leaf "(?)" glyph and the switcher's needs-input-first sort both key on) — a coordinator header's ROLLED-UP subtree signal (`SubtreeNeedsInput`) does NOT independently qualify the header, since a descendant that actually needs input is its own, separate candidate; a coordinator header's OWN needs-input signal (`CoordRole().needsInputOwn()` — the coordinator's own live session or `blocked` status, as opposed to any descendant's rollup) DOES independently qualify the header, whether the orchestrator is a top-level root or a coordinator-spawned nested sub-team sharing its coordinator agent with a parent orchestrator — both render through the identical header-only row, and neither is a role-bearing row, so both are reached through the header rather than a role match. The scan starts strictly after the current cursor position and wraps around the whole ring back to (and including) the cursor itself, so repeated presses cycle forward through every candidate in turn without repeatedly landing on the one just visited, and a single remaining candidate keeps re-selecting itself once the ring wraps all the way around. Landing on a candidate reuses the existing ancestor-expand + reattach-a-dead/suspended-session + focus sequence (`JumpToTask`) the switcher's own Hera landing already uses — no new selection/expansion/focus logic; landing on a coordinator header resolves through the same `SelectByTaskID` primitive, extended to match a header row via its coordinator's own task id, and focus lands in the coordinator pane exactly as manual cursor navigation onto that header already does today.

When a coordinator-spawned sub-team's parent and child orchestrators share the SAME underlying coordinator task (one coordinator agent driving both), and that task needs input, both header rows independently qualify as candidates; landing resolves to whichever one `SelectByTaskID` matches first in rail row order — the same first-match convention already governing any other multi-binding task reachable through two role rows. This is a known, accepted characteristic of the existing multi-binding model, not a defect this requirement guards against.

Derived from: `internal/tui/hera/rail.go` (`railRow.needsInputTaskID`, `Rail.NextNeedsInputTaskID`, `Rail.SelectByTaskID`), `internal/tui/hera/page.go` (`HeraPage.JumpToNextNeedsInput`), `internal/tui/app.go` (`jumpToNextNeedsInput`).

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

#### Scenario: A top-level coordinator's own need is a reachable candidate

- **WHEN** a top-level orchestrator's coordinator role itself needs input and no other role does
- **THEN** `ctrl+g` finds it and lands the selection on the orchestrator's header row, moving focus to the coordinator pane — the header still shows the same rolled-up "(?)" glyph it always did

#### Scenario: A coordinator-spawned nested sub-team's own need is reachable

- **WHEN** a coordinator-spawned nested sub-team's coordinator role itself needs input (the parent orchestrator's coordinator agent also drives this child orchestrator) and no other role does
- **THEN** `ctrl+g` finds it (via the child's own signal) and expands any collapsed ancestor; because the parent and child headers share the identical underlying coordinator task, landing resolves to whichever of the two `SelectByTaskID` matches first in rail row order — structurally always the parent header, since a header row is placed before any of its nested children's rows — moving focus to the coordinator pane for that (shared) task. This is the same first-match convention the next scenario documents, not a distinct landing rule.

#### Scenario: A nested sub-coordinator bridging as a worker row remains reachable

- **WHEN** a nested sub-coordinator that bridges as an ordinary worker row in its parent orchestrator (not a coordinator-spawned header) itself needs input
- **THEN** `ctrl+g` finds it and lands the jump there like any other role-bearing row

#### Scenario: A shared coordinator task's duplicate header candidates resolve to the first match

- **WHEN** a coordinator-spawned sub-team's parent and child headers both qualify because they share the same underlying coordinator task, and that task needs input
- **THEN** `ctrl+g` lands on whichever of the two headers `SelectByTaskID` matches first in rail row order, consistently — matching the existing multi-binding convention rather than alternating between them

## REMOVED Requirements

### Requirement: A top-level coordinator's own needs-input signal is not a ctrl+g jump target

**Reason**: This requirement documented a deliberate exclusion that has been reversed. A coordinator's own needs-input signal — for a top-level orchestrator or a coordinator-spawned nested sub-team — is now a reachable `ctrl+g` candidate via the header-row match `SelectByTaskID` gained; see the "ctrl+g jumps to the next role needing input in rail order, cycling on repeat" requirement above, which now covers this case directly.

**Migration**: No caller-visible migration. Any code or test that asserted `ctrl+g`/`SelectByTaskID` cannot reach a coordinator's own task id should expect the opposite going forward.
