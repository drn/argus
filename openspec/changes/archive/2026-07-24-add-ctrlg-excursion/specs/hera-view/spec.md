# Hera View

## MODIFIED Requirements

### Requirement: ctrl+g jumps to the next role needing input in rail order, cycling on repeat

The system SHALL provide a dedicated, popup-free jump directly to the next role needing input — independent of the unified task/role switcher (`ctrl+j`) — reachable from the classic fullscreen agent view, the plain Tasks tab, and every Hera focus region (rail, coordinator pane, agent pane). The candidate ring is today's built rail order (Pinned → Active depth-first → Freelance → Archive), including any row the rail's partial-fold reveal already surfaces behind a closed ancestor fold. A candidate row's OWN needs-input signal qualifies it (the same signal the rail's leaf "(?)" glyph and the switcher's needs-input-first sort both key on) — a coordinator header's ROLLED-UP subtree signal (`SubtreeNeedsInput`) does NOT independently qualify the header, since a descendant that actually needs input is its own, separate candidate; a coordinator header's OWN needs-input signal (`CoordRole().needsInputOwn()` — the coordinator's own live session or `blocked` status, as opposed to any descendant's rollup) DOES independently qualify the header, whether the orchestrator is a top-level root or a coordinator-spawned nested sub-team sharing its coordinator agent with a parent orchestrator — both render through the identical header-only row, and neither is a role-bearing row, so both are reached through the header rather than a role match. The scan starts strictly after the current cursor position and wraps around the whole ring back to (and including) the cursor itself, so repeated presses cycle forward through every candidate in turn without repeatedly landing on the one just visited, and a single remaining candidate keeps re-selecting itself once the ring wraps all the way around. Landing on a candidate reuses the existing ancestor-expand + reattach-a-dead/suspended-session + focus sequence (`JumpToTask`) the switcher's own Hera landing already uses — no new selection/expansion/focus logic; landing on a coordinator header resolves through the same `SelectByTaskID` primitive, extended to match a header row via its coordinator's own task id, and focus lands in the coordinator pane exactly as manual cursor navigation onto that header already does today.

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

## ADDED Requirements

### Requirement: The rail captures a problem-child excursion snapshot at the needs-input 0→≥1 transition

The system SHALL maintain, in-memory only (never persisted, never part of the `hera.rail_view_state` DB blob), an optional excursion snapshot capturing every orchestrator's fold/collapse state, the per-coordinator archive-expando state, the freelance/archive section fold state, the focused kanban group, and the previously-selected role/orchestrator. This snapshot SHALL be captured the instant a fold-independent whole-rail needs-input count (every role's own needs-input signal across every orchestrator, including coordinator-kind roles, plus freelance roles — regardless of fold, archive-section, or kanban-group-focus state) transitions from zero to one or more — NEVER at the moment a jump key is pressed, so the captured layout reflects the operator's true state immediately before the interruption rather than anything they have since done in reaction to it. A capture taken while a snapshot is ALREADY held (a second or further needs-input signal appearing before the first excursion has been discharged) SHALL be suppressed — the existing snapshot survives — UNLESS no snapshot is currently held despite the count already being one or more, which happens only immediately after an explicit restore fired while problems remained outstanding (see the restore requirement below); that case SHALL re-arm a fresh capture from the rail's current state.

Derived from: `internal/tui/hera/rail.go` (`Rail.noteExcursionTransition`, `Rail.captureExcursionSnapshot`, `Rail.EnsureExcursionArmed`, `railSnapshot`), `internal/tui/hera/model.go` (`Model.NeedsInputTotalCount`).

#### Scenario: A fresh interruption after being fully at rest captures a snapshot

- **WHEN** the whole-rail needs-input count is zero, the operator has the rail folded in some particular way, and a role's needs-input signal newly becomes true (count goes from zero to one)
- **THEN** the rail captures a snapshot of the current fold/selection state before anything else changes

#### Scenario: A second interruption during an open excursion does not overwrite the snapshot

- **WHEN** an excursion snapshot is already held (from an earlier 0→≥1 transition) and, while it is still held, a second, unrelated role's needs-input signal becomes true (count goes from one to two)
- **THEN** no new snapshot is captured — the ORIGINAL snapshot (from before the operator reacted to the first problem) is what a later restore re-applies, even if the operator changed the fold state in between

#### Scenario: An explicit restore while problems remain re-arms on the next interruption tick

- **WHEN** an excursion snapshot is discharged (via `ctrl+g` or `ctrl+b`) while the needs-input count is still one or more, and the rail subsequently rebuilds
- **THEN** a fresh snapshot is captured from the rail's fold/selection state as it stands at that rebuild, ready for a later restore to reproduce

#### Scenario: A resolved excursion is not auto-discharged

- **WHEN** an excursion snapshot is held and the needs-input count later drops back to zero on its own (every outstanding problem resolves)
- **THEN** the snapshot remains held until the operator explicitly discharges it via `ctrl+g` or `ctrl+b` — nothing auto-clears it

### Requirement: ctrl+b manually restores the rail's excursion snapshot at any time

The system SHALL bind `ctrl+b` (global, unconditional dispatch — reachable from the classic fullscreen agent view, the plain Tasks tab, and every Hera focus region, identically to `ctrl+g`/`ctrl+k`) to a manual "restore rail" action: if an excursion snapshot is currently held, it is re-applied (fold/collapse state, per-coordinator archive-expando state, freelance/archive section state, focused kanban group, and prior selection) and then discarded, and a "Rail restored" notice is shown. This action is unconditional on the current needs-input count — unlike `ctrl+g`'s restore branch, which only fires once the count has dropped to zero, `ctrl+b` discharges the excursion regardless of how many problems remain outstanding. Restoring SHALL NOT mark any outstanding problem as resolved, switch the active tab, or tear down a live fullscreen agent view — it only ever changes fold/selection state, so a subsequent `ctrl+g` can still reach any needs-input role that remains. When no snapshot is held, `ctrl+b` SHALL be a silent no-op (no notice, no navigation).

Derived from: `internal/tui/hera/rail.go` (`Rail.RestoreExcursion`), `internal/tui/app.go` (`restoreHeraRailExcursion`), `internal/tui/keymap/actions.go` (`ActGlobalRestoreRail`, default `ctrl+b`).

#### Scenario: Manual restore while problems remain outstanding

- **WHEN** an excursion snapshot is held and one or more roles still need input, and the user presses `ctrl+b`
- **THEN** the rail's fold/selection state is restored to the snapshot, the snapshot is discarded, and a "Rail restored" notice is shown, without switching tabs or leaving a live fullscreen agent view

#### Scenario: A still-outstanding problem remains reachable after a manual restore

- **WHEN** the user presses `ctrl+b` while a role still needs input, discharging the excursion snapshot
- **THEN** a subsequent `ctrl+g` still finds and jumps to that role — the manual restore did not discharge the candidate ring, only the fold snapshot

#### Scenario: Manual restore is reachable from a focused Hera pane or the classic agent view

- **WHEN** a Hera coordinator/worker terminal pane holds focus, or the classic fullscreen agent view is active, and the user presses `ctrl+b`
- **THEN** the restore fires and the byte never reaches the pane's live PTY or the agent session

#### Scenario: Manual restore is a silent no-op when nothing is held

- **WHEN** the user presses `ctrl+b` and no excursion snapshot is currently held (never opened, or already discharged)
- **THEN** nothing happens — no notice, no navigation, no crash
