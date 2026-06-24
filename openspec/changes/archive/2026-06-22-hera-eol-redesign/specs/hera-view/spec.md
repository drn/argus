# hera-view delta: end-of-life redesign — hide vs nuke

## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Left walks to the parent; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` HIDES the selected worker / sub-coordinator into its parent coordinator's nested archive (Tier 1 — reversible, keeps the session + worktree alive); `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `J` adopts a freelancer / re-parents a coordinator; `C` clears the selected coordinator's archive (NUKES every Tier-1 hidden item under it); `Ctrl+Z` fullscreens the focused pane; `/` filters the rail by name; `Ctrl+D` NUKES the selected role/orchestrator (Tier 2 — removes it and its whole subtree from the rail, reclaims worktrees). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

The rail SHALL NOT bind `R` (retire) or a rail-wide `Ctrl+R` (prune) — both are removed by this redesign. All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `J`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` is selection-INDEPENDENT and fires even on an empty rail.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/heraactions.go` (handlers), `internal/tui/modal/help.go` (help overlay Hera section).

`NOTE:` `Ctrl+D` is the only delete/nuke key and never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. A focused content pane forwards `C`/`a`/`Ctrl+D` to its PTY.

#### Scenario: Hide key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected worker
- **THEN** the hide callback fires for that worker's `(role, orchestrator)` selection (no confirmation) and the key does not leak to navigation

#### Scenario: Retire and rail-wide prune keys are unbound

- **WHEN** the user presses `R` or `Ctrl+R` while the rail is focused
- **THEN** nothing end-of-life happens (`R` is unbound; `Ctrl+R` is not a rail-wide prune) — the redesign removed both

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Left, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a` (hide), `P`, `s`/`S`, `J`, `C` (clear archive), `Ctrl+Z`, `/`, and `Ctrl+D` (nuke), and does NOT list `R` or a rail-wide `Ctrl+R`

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` in the Hera rail is the NUKE (Tier 2) action. It SHALL NEVER hard-delete a DB row. It marks the row NUKED (a `nuked_at` stamp that removes it from the rail entirely — it is NOT shown in any visible archive) and reclaims only the real resources. Specifically, nuke:

- marks the hera role row(s) NUKED and ENDS their live binding (never `DeleteHeraRole`);
- marks the orchestrator row NUKED (for a coordinator/header or whole-subtree nuke), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`), never `db.Delete`;
- retains the role's inbox/messages — because the role row is retained (only stamped nuked/archived), its messages stay attached as history (no message rows are deleted, no message-archive column is required, and a nuked role's inbox stays readable);
- RECLAIMS only the real resources: stops the session and removes the worktree + LOCAL and REMOTE branch.

Nuking a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept). The role row is marked nuked + its binding ended either way.

Nuking a COORDINATOR / orchestrator HEADER SHALL cascade the SAME mark-nuked-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are marked nuked + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone).

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (including any internal-bridge worktree between two subtree orchestrators), and how many tasks are preserved.

The difference from the `a` HIDE key: `a` HIDES (Tier 1) — the row moves into its parent coordinator's nested archive and the worktree/session stay ALIVE, fully reversible; `Ctrl+D` NUKES (Tier 2) — the row leaves the rail entirely and its worktree/session are reclaimed, recoverable only via the DB.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraNukeRole`, `heraReclaimAndArchiveTask`, `heraCascadeNukeFrom`, `heraDoCascadeNuke`, `heraTaskBoundOutside`), `internal/tui/hera/ops.go` (`NukeRole`, `NukeOrchestrator`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`), `context/knowledge/gotchas/hera-view.md`.

`NOTE:` NET zero hard deletes from any hera table — every nuked role, orchestrator, inbox, and task row is retained and retrievable via the DB. The one remaining non-hera delete (`db.SetArchived` dropping a task's queued LEGACY `task_messages`, a different table) is established archive behavior and out of scope.

#### Scenario: Nuking a sole-bound role removes it from the rail and reclaims its worktree

- **WHEN** a role is nuked and its task has exactly one live binding
- **THEN** the session is stopped, the worktree + local and remote branch are reclaimed, the role row is marked NUKED (invisible to the rail) with its binding ended, and the argus task row is ARCHIVED — none are hard-deleted

#### Scenario: Nuking a multi-bound role preserves the task

- **WHEN** a role is nuked and its task holds live bindings in more than one orchestrator
- **THEN** the role row is marked nuked + its binding ended; the task is left fully alone (not archived, worktree kept) and its other-orchestrator binding survives

#### Scenario: Nuking a coordinator cascades over the full subtree and reclaims worktrees

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header and the operator confirms
- **THEN** that orchestrator, every nested sub-coordinator, and all their agents are marked NUKED (removed from the rail) — sessions stopped and each sole-bound task's worktree + local and remote branch reclaimed — with nothing hard-deleted (rows retained, inboxes readable)
- **AND** a task bound live in an orchestrator outside the subtree is preserved (left fully alone)

#### Scenario: Cascade confirm states the counts

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header
- **THEN** a confirmation modal opens stating how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (counting the internal-bridge worktree in a multi-level subtree), and how many tasks are preserved

## ADDED Requirements

### Requirement: Two resting states — hide (Tier 1) and nuke (Tier 2) (area 7)

The Hera rail SHALL offer exactly two end-of-life resting states for a role or orchestrator, both of which retain every DB row (the bedrock rule: a hera row is never hard-deleted):

- **HIDDEN (Tier 1)** — reached by `a`. The row is `archived_at`-stamped on the hera ROLE only (NOT `nuked_at`, and NOT `db.SetArchived` on the argus task — HIDE is rail-only, so the worker keeps running and still shows in the Tasks tab) and renders inside its PARENT coordinator's nested "Archive (N)" expando. Hiding a bridging sub-coordinator collapses its WHOLE subtree into that expando (structure retained — the sub-coord's agents nest beneath it inside the expando), never leaking a descendant to a top-level root. The worktree and session stay ALIVE (no detach). It is a reversible toggle (un-hide restores it exactly to the rail) and is NOT confirmed. `a` applies to a WORKER or a sub-coordinator only; on a top-level coordinator (no parent to nest under) it surfaces feedback and is a no-op.
- **NUKED (Tier 2)** — reached by `Ctrl+D` (any worker or coordinator, including top-level) or `C` (the selected coordinator's hidden descendants). The row is `nuked_at`-stamped and is REMOVED from the rail entirely — it appears in no visible archive. Its worktree + local/remote branch are reclaimed from disk and its session stopped; its DB rows (role + orchestrator + inbox + argus task) are retained. Recovery is via the DB only (re-spin a fresh worktree); a nuked role's inbox stays readable.

`C` SHALL be scoped to the SELECTED coordinator's archive — it nukes every Tier-1 hidden item under that coordinator (equivalent to `Ctrl+D` on each), never a global sweep, and is confirmed with a count. When the selected coordinator's archive is empty it surfaces "nothing to clear" and opens no confirm.

Derived from: `internal/tui/heraactions.go` (`heraHide`, `heraClearArchive`), `internal/tui/hera/ops.go` (`Hide`/`Unhide` toggle, `NukeRole`/`NukeOrchestrator`), `internal/tui/hera/model.go` (BuildModel skips db rows with `nuked_at` set), `internal/db/hera.go` (`nuked_at`).

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

- **WHEN** the user presses `C` on a coordinator that has Tier-1 hidden descendants and confirms
- **THEN** each hidden descendant is NUKED (worktree + branch reclaimed unless bound live elsewhere, role marked nuked, sole-bound task archived) and the confirm modal showed the count; a coordinator with an empty archive shows "nothing to clear" and opens no confirm
