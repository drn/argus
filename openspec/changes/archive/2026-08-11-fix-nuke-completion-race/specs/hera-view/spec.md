## MODIFIED Requirements

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` in the Hera rail is the NUKE (Tier 2) action. It SHALL NEVER hard-delete a DB row. It marks the row NUKED (a `nuked_at` stamp that removes it from the rail entirely — it is NOT shown in any visible archive) and reclaims only the real resources. Specifically, nuke:

- marks the hera role row(s) NUKED and ENDS their live binding (never `DeleteHeraRole`);
- marks the orchestrator row NUKED (for a coordinator/header or whole-subtree nuke), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`), never `db.Delete`;
- ADVANCES the argus task's status from in_review to complete WHEN the task is currently in_review AT THE MOMENT OF ARCHIVE — never synchronously when it is pending or in_progress (a still-active or never-reviewed task is archived with its status left untouched at that instant), and it is a no-op when the task is already complete. This applies identically regardless of which hera role kind (coordinator, worker, or freelance) is being nuked, since the check reads the task's own status column, not the hera role's kind or status;
- for a task that IS in_progress (actively live) at the moment of reclaim, GUARANTEES it still reaches complete once the reclaim's own session stop (below) actually exits — even though the moment-of-archive snapshot above leaves its status untouched. Reclaim marks such a task as awaiting forced completion before backgrounding its stop; the session-exit handler that later observes that stop's exit (an unavoidably asynchronous event, since the stop itself is backgrounded to avoid blocking the UI thread on a bulk cascade) consults that mark and lands the task at complete instead of the ordinary crash/stop/fast-fail → in_review rule that governs an otherwise-identical, non-reclaim exit. This does not override a task that still holds a live worker-kind hera binding at exit time — that invariant (a task never self-completes while worker-bound) always wins if it somehow still applies;
- retains the role's inbox/messages — because the role row is retained (only stamped nuked/archived), its messages stay attached as history (no message rows are deleted, no message-archive column is required, and a nuked role's inbox stays readable);
- RECLAIMS only the real resources: stops the session (backgrounded) and removes the worktree + LOCAL and REMOTE branch (also backgrounded).

Nuking a SINGLE ROLE (not a cascade) SHALL first run the merge-safety classifier's Tier A (local-only, no network) check against that role's task, computed off the UI thread, and open the merge-safety review popup (see "Merge-safety review popup") with that one task as its sole candidate, in place of a plain confirm — choosing to clean (via either popup action, which are equivalent at a single candidate) proceeds with the nuke exactly as described above; choosing Cancel aborts it. This is a WARNING, never a gate: a not-confirmed-merged task can still be cleaned via the popup's override action.

Nuking a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept, status untouched). The role row is marked nuked + its binding ended either way.

Nuking a COORDINATOR / orchestrator HEADER SHALL cascade the SAME mark-nuked-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are marked nuked + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone). This cascade path does NOT use the merge-safety review popup (see "Merge-safety review popup is scoped to single-role nuke and the global Cleanup action, not cascade or clear-archived") — it keeps its existing all-or-nothing confirm, augmented only with a confirmed/not-confirmed count.

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (including any internal-bridge worktree between two subtree orchestrators), how many tasks are preserved, and how many of the reclaimed tasks are confirmed merged vs. not confirmed (via the same Tier A check, computed off the UI thread before the confirm opens).

The difference from the `a` HIDE key: `a` HIDES (Tier 1) — the row moves into its parent coordinator's nested archive and the worktree/session stay ALIVE, fully reversible; `Ctrl+D` NUKES (Tier 2) — the row leaves the rail entirely and its worktree/session are reclaimed, recoverable only via the DB.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraNukeRole`, `heraReclaimAndArchiveTask`, `heraCascadeNukeFrom`, `heraDoCascadeNuke`, `heraTaskBoundOutside`), `internal/tui/app.go` (`handleSessionExitUI`, `pendingHeraReclaim`), `internal/tui/hera/ops.go` (`NukeRole`, `NukeOrchestrator`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`, `RollHeraWorkerToReview`), `internal/db/tasks.go` (`SetStatus`), `internal/mergesafety` (Tier A classification), `context/knowledge/gotchas/hera-view.md`.

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

#### Scenario: Reclaiming a still-active task leaves its status untouched at the moment of archive

- **WHEN** a task with status `pending` or `in_progress` is reclaimed and archived by a nuke
- **THEN** the task is archived exactly as before and its status is left unchanged AT THAT INSTANT — it is NOT synchronously advanced to `complete`

#### Scenario: Reclaiming a live task still completes once its stop settles

- **WHEN** a task with status `in_progress` is reclaimed and archived by a nuke, and the session stop that reclaim fired (backgrounded) subsequently exits
- **THEN** the task's status lands at `complete` — never the ordinary `in_review` that an otherwise-identical, non-reclaim stop/crash would produce — once that exit is observed, even though the archive itself left the status column untouched at the moment of reclaim
- **AND** this applies regardless of whether the exit is reported as clean or non-clean, since a deliberate reclaim stop is terminal-by-design either way

#### Scenario: A reclaimed task still holding a live worker-kind hera binding is not force-completed

- **WHEN** a task's reclaim-triggered exit lands while the task still holds a live worker-kind hera binding (the PR #707 / BUG-050 invariant's precondition)
- **THEN** the task rolls to `in_review` via the existing worker-finish policy, never `complete` — the invariant wins over the forced-completion guarantee above

#### Scenario: Reclaiming an already-complete task is idempotent

- **WHEN** a task with status `complete` is reclaimed and archived by a nuke
- **THEN** the task is archived and its status remains `complete` (no-op status write)

#### Scenario: The status advancement is uniform across role kinds

- **WHEN** the reclaimed task's bound role is a coordinator, a worker, or a freelance role, and the task's status is `in_review` at the moment of reclaim
- **THEN** the status is advanced to `complete` in every case — the decision depends only on the task's own status column, not on the role's kind

#### Scenario: Single-role nuke opens the review popup instead of a plain confirm

- **WHEN** a role is nuked and its task has exactly one live binding
- **THEN** the merge-safety review popup opens with that task as its sole candidate, sectioned as SAFE or NOT-SAFE per the Tier A check, instead of a plain y/N confirm

#### Scenario: A not-confirmed single-role nuke can still be cleaned via the override
- **WHEN** the single candidate in the popup is NOT-SAFE
- **THEN** `Clean safe` acts on nothing, and `Clean all` proceeds with the nuke exactly as if it had been confirmed merged — the operator is never blocked

#### Scenario: The merge-safety check runs off the UI thread and does not call the network for a nuke
- **WHEN** any nuke's merge-safety check is being prepared (single-role or cascade)
- **THEN** it runs in a background goroutine, invokes no `gh`/GitHub network call, and the relevant popup/confirm only opens once the check completes
