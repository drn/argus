# Hera View

## MODIFIED Requirements

### Requirement: Two resting states — hide (Tier 1) and nuke (Tier 2) (area 7)

The Hera rail SHALL offer exactly two end-of-life resting states for a role or orchestrator, both of which retain every DB row (the bedrock rule: a hera row is never hard-deleted):

- **HIDDEN (Tier 1)** — reached by `a`. The row is `archived_at`-stamped on the hera ROLE only (NOT `nuked_at`, and NOT `db.SetArchived` on the argus task — HIDE is rail-only, so the worker keeps running and still shows in the Tasks tab) and renders inside its PARENT coordinator's nested "Archive (N)" expando. Hiding a bridging sub-coordinator collapses its WHOLE subtree into that expando (structure retained — the sub-coord's agents nest beneath it inside the expando), never leaking a descendant to a top-level root. The worktree and session stay ALIVE (no detach). It is a reversible toggle (un-hide restores it exactly to the rail) and is NOT confirmed. `a` applies to a WORKER or a sub-coordinator only; on a top-level coordinator (no parent to nest under) it surfaces feedback and is a no-op.
- **NUKED (Tier 2)** — reached by `Ctrl+D` (any worker or coordinator, including top-level) or `C` (the selected coordinator's hidden descendants). The row is `nuked_at`-stamped and is REMOVED from the rail entirely — it appears in no visible archive. Its worktree + local/remote branch are reclaimed from disk and its session stopped; its DB rows (role + orchestrator + inbox + argus task) are retained. Recovery is via the DB only (re-spin a fresh worktree); a nuked role's inbox stays readable.

`C` SHALL be scoped to the SELECTED coordinator's archive — it nukes every Tier-1 hidden item under that coordinator (equivalent to `Ctrl+D` on each), never a global sweep, and is confirmed with a count. When a hidden item is a bridging row into a nested sub-coordinator, `C` SHALL ALSO fully cascade-delete that child orchestrator's whole subtree (the child coordinator, its agents, and any further nested sub-teams) — the same subtree teardown `Ctrl+D` performs on a live bridge — in addition to ending the bridging row's own binding; ending only that one binding would leave the child orchestrator with no parent link, causing it to reappear as a new top-level root on the next rail rebuild instead of being removed with the rest of the archive. When the selected coordinator's archive is empty (no hidden leaf workers AND no hidden bridges) it surfaces "nothing to clear" and opens no confirm.

Derived from: `internal/tui/heraactions.go` (`heraHide`, `heraClearArchive`, `countCascadeSubtree`), `internal/tui/hera/ops.go` (`Hide`/`Unhide` toggle, `NukeRole`/`NukeOrchestrator`), `internal/tui/hera/model.go` (`BuildModel` skips db rows with `nuked_at` set), `internal/tui/hera/eol.go` (`SubtreeArchivedWorkers`, `SubtreeArchivedBridges`), `internal/db/hera.go` (`nuked_at`).

#### Scenario: Hide keeps the session and worktree alive (rail-only)

- **WHEN** the user presses `a` on a live worker
- **THEN** the worker's hera role is archived and renders in its parent coordinator's nested archive expando, while its session, worktree, and argus task row are all left untouched (the task still appears in the Tasks tab), and pressing `a` again un-hides it exactly

#### Scenario: Hiding a sub-coordinator collapses its subtree into the parent's archive

- **WHEN** the user presses `a` on a bridging sub-coordinator that has its own nested agents
- **THEN** the sub-coordinator and its whole subtree fold into the parent coordinator's "Archive (N)" expando — its agents render nested beneath it inside the expando when it is opened, are hidden when it is collapsed, and are never hoisted to a top-level root in either fold state

#### Scenario: Hide on a top-level coordinator is feedback-only

- **WHEN** the user presses `a` on a top-level coordinator / orchestrator header
- **THEN** the status bar shows a "hide applies to workers and sub-coordinators" message and nothing is changed (a top-level coordinator has no parent archive to nest under)

#### Scenario: Nuked rows are invisible to the rail

- **WHEN** a role or orchestrator is marked `nuked_at`
- **THEN** BuildModel omits it from every rail section (it is not shown in any archive); its DB row, inbox, and argus task are still retrievable from the DB

#### Scenario: Clear-this-coordinator's-archive nukes the hidden descendants

- **WHEN** the user presses `C` on a coordinator that has Tier-1 hidden LEAF descendants and confirms
- **THEN** each hidden leaf descendant is NUKED (worktree + branch reclaimed unless bound live elsewhere, role marked nuked, sole-bound task archived) and the confirm modal showed the count; a coordinator with an empty archive shows "nothing to clear" and opens no confirm

#### Scenario: Clear-this-coordinator's-archive cascades a hidden nested sub-team

- **WHEN** the user presses `C` on a coordinator whose archive contains a bridging row into a nested sub-coordinator (hidden via `a` on that sub-coordinator) and confirms
- **THEN** the bridging row's own binding is ended AND the child orchestrator's whole subtree (its coordinator, its agents, and any further nested sub-teams) is fully NUKED — the child does not survive as an orphan and does not reappear as a new top-level orchestrator on the next rail rebuild; the confirm modal reports both the flat hidden-agent count and the nested sub-team(s) being removed
