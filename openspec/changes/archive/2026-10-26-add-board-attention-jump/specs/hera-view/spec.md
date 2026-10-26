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

When `ui.coordinator_board` is enabled, the candidate ring SHALL additionally include every coordinator with unread board items, including top-level headers and nested sub-coordinator bridging rows. A coordinator qualifying through both signals SHALL appear once. Board targets SHALL land on that coordinator's exact row, even when its task is also bound in another orchestrator, and run the existing selection-time board auto-landing behavior. A repeated jump to the already-selected unread coordinator SHALL also show its board. Folded paths SHALL reveal board candidates through the existing partial-fold mechanism and expand ancestors on landing. The Ctrl+J popup's next-needs-input scan SHALL use the same qualification, preserving its existing all-kanban-group traversal. With the setting disabled, both scans SHALL retain their previous needs-input-only behavior.

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


### Requirement: The rail captures a problem-child excursion snapshot at the needs-input 0→≥1 transition, re-arming only on a genuinely new distinct interruption

The system SHALL maintain, in-memory only (never persisted, never part of the `hera.rail_view_state` DB blob), an optional excursion snapshot capturing every orchestrator's fold/collapse state, the per-coordinator archive-expando state, the freelance/archive section fold state, the focused kanban group, and the previously-selected role/orchestrator. This snapshot SHALL be captured the instant a fold-independent whole-rail needs-input SET (every role's own needs-input signal across every orchestrator, including coordinator-kind roles, plus freelance roles — regardless of fold, archive-section, or kanban-group-focus state — tracked by role identity, not merely counted) transitions from empty to non-empty — NEVER at the moment a jump key is pressed, so the captured layout reflects the operator's true state immediately before the interruption rather than anything they have since done in reaction to it. A capture taken while a snapshot is ALREADY held (a second or further needs-input signal appearing before the first excursion has been discharged) SHALL be suppressed — the existing snapshot survives.

The empty→non-empty transition above SHALL NOT be evaluated on the rail's own first-ever rebuild (before its row layout has been built even once, e.g. immediately after a TUI launch/relaunch). At that instant there is no prior rendered layout to treat as "the operator's true state immediately before the interruption" — the fold state reflects whatever a PREVIOUS session persisted to disk, not anything from the current one, and there are no rows yet for the selection to anchor to. If a needs-input role already exists at that first rebuild (a stale, pre-existing "?" that predates the launch), the system SHALL only seed its needs-input-set tracking from the current set, without capturing a snapshot; the normal transition/re-arm rules apply from the second rebuild onward. This defers the eventual freeze to either a genuinely new distinct interruption (once the rail has real rows to work with) or to `ctrl+g`'s own belt-and-suspenders arm at keypress time — both of which reflect the operator's actual current-session position, unlike a first-rebuild capture.

When no snapshot is held despite the needs-input set being non-empty — reachable immediately after an explicit restore (`ctrl+g`/`ctrl+b`) fired while some role still needed input, or during the deferral window described above — the system SHALL track the needs-input set by role identity and continuously refresh a live candidate (the rail's CURRENT fold/selection state) on every rebuild for as long as the set stays a subset of (or equal to) the set as of the last capture/refresh: the SAME still-outstanding role reappearing across rebuilds — however long it stays unresolved — SHALL NOT by itself trigger a re-arm, so any fold/selection change the operator makes in the meantime is tracked, not discarded. A re-arm — freezing a fresh snapshot from the rail's state as it stands at that rebuild — SHALL fire only the instant a role id appears in the needs-input set that was NOT present in the tracked baseline: a genuinely new, distinct interruption. Identity-based tracking (not a bare count) is required to make this distinction: a count cannot tell "the same stale problem, still unresolved" apart from "a different problem, newly arrived."

Derived from: `internal/tui/hera/rail.go` (`Rail.noteExcursionTransition`, `Rail.captureExcursionSnapshot`, `Rail.EnsureExcursionArmed`, `railSnapshot`, `hasNewNeedsInputID`), `internal/tui/hera/model.go` (`Model.NeedsInputTotalCount`, `Model.needsInputRoleIDs`).

#### Scenario: A fresh interruption after being fully at rest captures a snapshot

- **WHEN** the whole-rail needs-input set is empty, the operator has the rail folded in some particular way, and a role's needs-input signal newly becomes true (the set goes from empty to one role)
- **THEN** the rail captures a snapshot of the current fold/selection state before anything else changes

#### Scenario: A stale needs-input role already present at the rail's first-ever rebuild does not capture a bogus snapshot

- **WHEN** the rail's very first rebuild (right after a TUI launch/relaunch, before any row layout has ever been built) already observes a non-empty needs-input set — a role that needed input before the launch and never resolved
- **THEN** no snapshot is captured on that rebuild; the needs-input set is only recorded as the tracking baseline, and the operator's subsequent navigation this session is what an eventual freeze (a genuinely new distinct interruption, or `ctrl+g`'s own belt-and-suspenders arm) reflects — never the previous session's persisted fold state or an unresolved (zero) selection

#### Scenario: A second interruption during an open excursion does not overwrite the snapshot

- **WHEN** an excursion snapshot is already held (from an earlier empty→non-empty transition) and, while it is still held, a second, unrelated role's needs-input signal becomes true
- **THEN** no new snapshot is captured — the ORIGINAL snapshot (from before the operator reacted to the first problem) is what a later restore re-applies, even if the operator changed the fold state in between

#### Scenario: An explicit restore while one stale problem remains does not re-arm until a genuinely new one appears

- **WHEN** an excursion snapshot is discharged (via `ctrl+g` or `ctrl+b`) while a role still needs input, and the operator then navigates or folds the rail manually across any number of rebuilds while that SAME role remains the only one needing input
- **THEN** no snapshot is re-armed at any point during that span — the rail keeps tracking the operator's latest fold/selection state live — until a DIFFERENT, previously-unseen role's needs-input signal becomes true, at which point a fresh snapshot is captured from the rail's state as it stands at that moment (the operator's latest position, not the position at the time of the earlier restore)

#### Scenario: A role absorbed into an open excursion does not spuriously re-arm after a later discharge

- **WHEN** a role's needs-input signal becomes true WHILE an excursion is already held (folding into that same excursion per the scenario above) and, after a later explicit restore discharges it, that same role is still the only one needing input
- **THEN** the rebuild immediately following the discharge does NOT re-arm — the role is already accounted for in the tracked baseline, even though it was never individually captured by its own freeze

#### Scenario: A resolved excursion is not auto-discharged

- **WHEN** an excursion snapshot is held and the needs-input set later becomes empty on its own (every outstanding problem resolves)
- **THEN** the snapshot remains held until the operator explicitly discharges it via `ctrl+g` or `ctrl+b` — nothing auto-clears it

When `ui.coordinator_board` is enabled, the whole-rail excursion membership SHALL additionally include coordinator role identities with unread boards, deduplicating coordinators that also need input. This SHALL govern the global jump's attention count as well as identity-based arming. Existing cold-start deferral, absorption while frozen and explicit restoration SHALL apply equally to board candidates. Unread counts or additional items on an already-unread coordinator SHALL NOT constitute a new identity.

#### Scenario: New board coordinator re-arms but a stale board does not

- **WHEN** a snapshot was restored while one coordinator's board remains unread and rebuilds keep observing that board
- **THEN** no snapshot re-arms until a distinct coordinator newly has unread board items or another role newly needs input

#### Scenario: Count unchanged but coordinator identity changed

- **WHEN** one coordinator's unread board clears and another coordinator becomes unread before the next rebuild
- **THEN** the new coordinator identity re-arms the excursion even though the total attention count is unchanged
