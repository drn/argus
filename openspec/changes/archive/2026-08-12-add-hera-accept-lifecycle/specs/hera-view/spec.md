## MODIFIED Requirements

### Requirement: Two resting states — hide (Tier 1) and nuke (Tier 2) (area 7)

The Hera rail SHALL offer exactly two end-of-life resting states for a role or orchestrator, both of which retain every DB row (the bedrock rule: a hera row is never hard-deleted):

- **HIDDEN (Tier 1)** – reached by `a`. The row is `archived_at`-stamped on the hera ROLE only (NOT `nuked_at`, and NOT `db.SetArchived` on the argus task – HIDE is rail-only, so the worker's argus task row and its worktree stay ALIVE and it still shows in the Tasks tab) and renders inside its PARENT coordinator's nested "Archive (N)" expando. Hiding a bridging sub-coordinator collapses its WHOLE subtree into that expando (structure retained – the sub-coord's agents nest beneath it inside the expando), never leaking a descendant to a top-level root. On the HIDE direction only (the role was active and becomes archived), the system SHALL additionally stop the role's live agent session – freeing its memory – if one exists (`add-hera-accept-lifecycle`: freeing memory is a nice-to-have follow-through of the operator choosing to hide, never a requirement of Hera task completion itself, which stays a wholly separate concern). The stop is backgrounded (mirrors the nuke path's own `heraGoSafe` session-stop, so hiding many roles in quick succession never blocks the UI thread) and touches the SESSION ONLY – the worktree and local/remote branch are NEVER touched by hide, on either direction. The UN-HIDE direction (pressing `a` again) SHALL NEVER stop, restart, or otherwise touch any session – it is a pure row-level toggle. It is a reversible toggle (un-hide restores the role's rail visibility exactly; a stopped session can be brought back via the ordinary dead-session revive path, `Enter` on the row) and neither direction is confirmed. `a` applies to a WORKER or a sub-coordinator only; on a top-level coordinator (no parent to nest under) it surfaces feedback and is a no-op.
- **NUKED (Tier 2)** – reached by `Ctrl+D` (any worker or coordinator, including top-level) or `C` (the selected coordinator's hidden descendants). The row is `nuked_at`-stamped and is REMOVED from the rail entirely – it appears in no visible archive. Its worktree + local/remote branch are reclaimed from disk and its session stopped; its DB rows (role + orchestrator + inbox + argus task) are retained. Recovery is via the DB only (re-spin a fresh worktree); a nuked role's inbox stays readable.

`C` SHALL be scoped to the SELECTED coordinator's archive – it nukes every Tier-1 hidden item under that coordinator (equivalent to `Ctrl+D` on each), never a global sweep, and is confirmed with a count. When the selected coordinator's archive is empty it surfaces "nothing to clear" and opens no confirm.

Derived from: `internal/tui/heraactions.go` (`heraHide`), `internal/tui/hera/ops.go` (`ArchiveToggle` direction-reporting), `internal/tui/hera/model.go` (BuildModel skips db rows with `nuked_at` set), `internal/db/hera.go` (`nuked_at`).

#### Scenario: Hide keeps the worktree and argus task alive (rail-only)

- **WHEN** the user presses `a` on a live worker
- **THEN** the worker's hera role is archived and renders in its parent coordinator's nested archive expando, while its worktree and argus task row are left untouched (the task still appears in the Tasks tab), and pressing `a` again un-hides it exactly

#### Scenario: Hide stops the role's live session

- **WHEN** the user presses `a` on a role with a live agent session (the HIDE direction – the role was active immediately before the press)
- **THEN** the session is stopped in the background, while the worktree and local/remote branch are left completely untouched

#### Scenario: Hide with no live session is a clean no-op on the stop path

- **WHEN** the user presses `a` on a role with no live agent session
- **THEN** the role is archived exactly as usual and no stop attempt is made (nothing to stop)

#### Scenario: Un-hide never touches the session

- **WHEN** the user presses `a` on an already-hidden (archived) role (the UN-HIDE direction)
- **THEN** the role is unarchived and no session stop, restart, or any other session action occurs, regardless of whether a session happens to be live at that moment

#### Scenario: Hiding a sub-coordinator collapses its subtree into the parent's archive

- **WHEN** the user presses `a` on a bridging sub-coordinator that has its own nested agents
- **THEN** the sub-coordinator and its whole subtree fold into the parent coordinator's "Archive (N)" expando – its agents render nested beneath it inside the expando when it is opened, are hidden when it is collapsed, and are never hoisted to a top-level root in either fold state

#### Scenario: Hide on a top-level coordinator is feedback-only

- **WHEN** the user presses `a` on a top-level coordinator / orchestrator header
- **THEN** the status bar shows a "hide applies to workers and sub-coordinators" message and nothing is changed (a top-level coordinator has no parent archive to nest under)

#### Scenario: Nuked rows are invisible to the rail

- **WHEN** a role or orchestrator is marked `nuked_at`
- **THEN** BuildModel omits it from every rail section (it is not shown in any archive); its DB row, inbox, and argus task are still retrievable from the DB

#### Scenario: Clear-this-coordinator's-archive nukes the hidden descendants

- **WHEN** the user presses `C` on a coordinator that has Tier-1 hidden descendants and confirms
- **THEN** each hidden descendant is NUKED (worktree + branch reclaimed unless bound live elsewhere, role marked nuked, sole-bound task archived) and the confirm modal showed the count; a coordinator with an empty archive shows "nothing to clear" and opens no confirm

### Requirement: Agents roster renders as an aligned, scrollable table (area 6)

The Details pane's `Agents (N):` roster SHALL render as a compact, left-aligned table with four columns, in this order — **name** (the role name), **archetype** (the role's diligence archetype, `RoleView.Archetype`), **model** (the resolved LLM model applied to that role, `RoleView.AppliedModel`), and **status** (the existing status icon plus a short text label mirroring `widget.RoleStatusIcon`'s precedence: needs-input / working / accepted / ready / failed / done / idle / live / unbound, with a `PR` token composed in per the PR-indicator requirement) — preceded by a `NAME  ARCHETYPE  MODEL  STATUS` column-header row. Name leads so the identifying column reads immediately; status trails so its icon+label reads as a per-row trailing verdict rather than a leading marker. Archetype and model are read directly from the already-annotated `RoleView` the rail's model already carries (`Archetype` from the role row; `AppliedModel` stamped by `HeraPage`'s `tierResolver` during `doRefresh`, off the Draw path) — no additional daemon, store, or MCP read. A role with no resolved archetype or model (no profile consulted, or the CLI/backend default applied) renders `—` in that cell, never a blank.

`add-hera-accept-lifecycle` inserted the `accepted` rung: a worker whose bound task's `TaskStatus` is `complete` (coordinator-accepted via `hera_accept`, or the plan-DAG gater's auto-accept) reads `"accepted"` with a BOLD checkmark, distinct from a plain `"done"` worker's checkmark and a `"ready"` (self-reported `ready_to_close`) worker's clipboard-check icon — ranked below `needs-input`/`working` (a role still genuinely blocked or producing output shows that first) but above `ready`/`failed`/`done`/`idle`/`live` (a coordinator's accept is authoritative over the self-reported ladder). Wired via a new `RoleStatusInputs.Accepted` field (`role.TaskStatus == model.StatusComplete.String()`), consulted identically by `widget.RoleStatusIcon` (the rail's icon) and `rosterStatusText` (this roster's label), so the two never disagree.

Derived from: `internal/tui/hera/details.go` (`computeRosterColumns`, `rosterColStarts`, `rosterTruncate`, `archetypeDisplay`/`modelDisplay`, `rosterValueStyle`, `rosterStatusText`, `drawRosterHeader`, `drawRosterRow`, `ContentHeight`, `rosterScroll`/`rosterVisibleRows`, `rosterMaxScroll`, `clampRosterScroll`, `ScrollRoster`, `SetOrch`), `internal/tui/hera/rail.go` (`roleStatusInputs`), `internal/tui/widget/rolestatusicon.go` (`RoleStatusInputs.Accepted`, `RoleStatusIcon`), `internal/tui/hera/model.go` (`RoleView.Archetype`/`AppliedModel`/`TaskStatus`), `internal/tui/hera/page.go` (`SetTierResolver`, `doRefresh`, `handleDetailsKey`, `rosterScrollDelta`).

#### Scenario: Status icon and label never disagree with the rail

- **WHEN** a roster row's status cell renders its text label
- **THEN** the label is derived from the SAME precedence (`widget.RoleStatusIcon`'s inputs) that chose the row's status icon, so the two never contradict each other

#### Scenario: A coordinator-accepted worker reads distinctly from a merely self-reported ready_to_close one

- **WHEN** a worker's bound task's `TaskStatus` is `complete` (coordinator-accepted via `hera_accept`, or the plan-DAG gater's auto-accept), regardless of whether it also carries `ready_to_close`
- **THEN** its status cell reads `"accepted"` with a BOLD checkmark icon distinct from both a plain `"done"` worker's checkmark and a `"ready"` (self-reported `ready_to_close`) worker's clipboard-check icon — `"accepted"` outranks `ready` / `failed` / `done` / `idle` / `live`, but a role still genuinely `needs-input` or actively `working` shows that label first
