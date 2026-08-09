## MODIFIED Requirements

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` in the Hera rail is the NUKE (Tier 2) action. It SHALL NEVER hard-delete a DB row. It marks the row NUKED (a `nuked_at` stamp that removes it from the rail entirely — it is NOT shown in any visible archive) and reclaims only the real resources. Specifically, nuke:

- marks the hera role row(s) NUKED and ENDS their live binding (never `DeleteHeraRole`);
- marks the orchestrator row NUKED (for a coordinator/header or whole-subtree nuke), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`), never `db.Delete`;
- ADVANCES the argus task's status from in_review to complete WHEN the task is currently in_review at the moment of archive — never when it is pending or in_progress (a still-active or never-reviewed task is archived with its status left untouched), and it is a no-op when the task is already complete. This applies identically regardless of which hera role kind (coordinator, worker, or freelance) is being nuked, since the check reads the task's own status column, not the hera role's kind or status;
- retains the role's inbox/messages — because the role row is retained (only stamped nuked/archived), its messages stay attached as history (no message rows are deleted, no message-archive column is required, and a nuked role's inbox stays readable);
- RECLAIMS only the real resources: stops the session and removes the worktree + LOCAL and REMOTE branch.

Before the confirmation modal for any nuke opens, the system SHALL run the merge-safety classifier's Tier A (local-only, no network) check against every task about to be reclaimed, computed off the UI thread, and fold the result into the confirmation message: a confirmed-merged task's message is unchanged; a not-confirmed task's message states that its work could not be confirmed merged and proceeding may lose it. This is a WARNING ONLY — it SHALL NEVER block or gate the nuke; the operator can always proceed regardless of the verdict.

Nuking a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept, status untouched). The role row is marked nuked + its binding ended either way.

Nuking a COORDINATOR / orchestrator HEADER SHALL cascade the SAME mark-nuked-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are marked nuked + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone).

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (including any internal-bridge worktree between two subtree orchestrators), how many tasks are preserved, and — per the merge-safety check above — how many of the reclaimed tasks are confirmed merged vs. not confirmed.

The difference from the `a` HIDE key: `a` HIDES (Tier 1) — the row moves into its parent coordinator's nested archive and the worktree/session stay ALIVE, fully reversible; `Ctrl+D` NUKES (Tier 2) — the row leaves the rail entirely and its worktree/session are reclaimed, recoverable only via the DB.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraNukeRole`, `heraReclaimAndArchiveTask`, `heraCascadeNukeFrom`, `heraDoCascadeNuke`, `heraTaskBoundOutside`), `internal/tui/hera/ops.go` (`NukeRole`, `NukeOrchestrator`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`), `internal/db/tasks.go` (`SetStatus`), `internal/mergesafety` (Tier A classification), `context/knowledge/gotchas/hera-view.md`.

`NOTE:` NET zero hard deletes from any hera table — every nuked role, orchestrator, inbox, and task row is retained and retrievable via the DB. The one remaining non-hera delete (`db.SetArchived` dropping a task's queued LEGACY `task_messages`, a different table) is established archive behavior and out of scope.

#### Scenario: Nuking a sole-bound role removes it from the rail and reclaims its worktree

- **WHEN** a role is nuked and its task has exactly one live binding
- **THEN** the session is stopped, the worktree + local and remote branch are reclaimed, the role row is marked NUKED (invisible to the rail) with its binding ended, and the argus task row is ARCHIVED — none are hard-deleted

#### Scenario: Nuking a multi-bound role preserves the task

- **WHEN** a role is nuked and its task holds live bindings in more than one orchestrator
- **THEN** the role row is marked nuked + its binding ended; the task is left fully alone (not archived, worktree kept, status untouched) and its other-orchestrator binding survives

#### Scenario: Nuking a coordinator cascades over the full subtree and reclaims worktrees

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header and the operator confirms
- **THEN** that orchestrator, every nested sub-coordinator, and all their agents are marked NUKED (removed from the rail) — sessions stopped and each sole-bound task's worktree + local and remote branch reclaimed — with nothing hard-deleted (rows retained, inboxes readable)
- **AND** a task bound live in an orchestrator outside the subtree is preserved (left fully alone)

#### Scenario: Cascade confirm states the counts

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header
- **THEN** a confirmation modal opens stating how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (counting the internal-bridge worktree in a multi-level subtree), how many tasks are preserved, and how many of the reclaimed tasks are confirmed merged vs. not confirmed

#### Scenario: Reclaiming an in_review task advances it to complete

- **WHEN** a task with status `in_review` is reclaimed and archived by a nuke (a sole-bound role nuke, a cascade, or clearing a hidden archive)
- **THEN** the task's status is advanced to `complete` in addition to being archived

#### Scenario: Reclaiming a still-active task leaves its status untouched

- **WHEN** a task with status `pending` or `in_progress` is reclaimed and archived by a nuke
- **THEN** the task is archived exactly as before and its status is left unchanged — it is NOT advanced to `complete`

#### Scenario: Reclaiming an already-complete task is idempotent

- **WHEN** a task with status `complete` is reclaimed and archived by a nuke
- **THEN** the task is archived and its status remains `complete` (no-op status write)

#### Scenario: The status advancement is uniform across role kinds

- **WHEN** the reclaimed task's bound role is a coordinator, a worker, or a freelance role, and the task's status is `in_review` at the moment of reclaim
- **THEN** the status is advanced to `complete` in every case — the decision depends only on the task's own status column, not on the role's kind

#### Scenario: Single-role nuke confirm warns when merge status can't be confirmed

- **WHEN** a role is nuked, its task has exactly one live binding, and the merge-safety classifier's Tier A check does not confirm the task's branch as merged
- **THEN** the confirmation message states that the branch's work could not be confirmed merged and that proceeding may lose it, but the operator can still confirm and proceed

#### Scenario: Single-role nuke confirm is unchanged when merge status is confirmed

- **WHEN** a role is nuked, its task has exactly one live binding, and the merge-safety classifier's Tier A check confirms the task's branch as merged
- **THEN** the confirmation message is unchanged from today's wording — no warning is added

#### Scenario: The merge-safety check never blocks a nuke

- **WHEN** the merge-safety classifier reports a task as not confirmed merged, at any nuke entry point
- **THEN** the operator is still able to confirm and proceed with the nuke exactly as if it had been confirmed merged — the check only changes the message, never the available actions

#### Scenario: The merge-safety check runs off the UI thread and does not call the network
- **WHEN** any nuke confirmation is being prepared
- **THEN** the merge-safety check runs in a background goroutine, invokes no `gh`/GitHub network call, and the confirmation modal is only opened once the check completes
