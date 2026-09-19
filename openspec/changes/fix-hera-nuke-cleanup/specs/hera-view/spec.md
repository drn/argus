## MODIFIED Requirements

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` in the Hera rail is the NUKE (Tier 2) action. It SHALL NEVER hard-delete a hera row (role, orchestrator, or binding). It marks the row NUKED (a `nuked_at` stamp that removes it from the rail entirely — it is NOT shown in any visible archive) and reclaims the real resources, INCLUDING the argus task row itself once that reclaim is fully confirmed. Specifically, nuke:

- marks the hera role row(s) NUKED and ENDS their live binding (never `DeleteHeraRole`);
- marks the orchestrator row NUKED (for a coordinator/header or whole-subtree nuke), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`) immediately, as before;
- ADVANCES the argus task's status from in_review to complete WHEN the task is currently in_review AT THE MOMENT OF ARCHIVE — never synchronously when it is pending or in_progress (a still-active or never-reviewed task is archived with its status left untouched at that instant), and it is a no-op when the task is already complete. This applies identically regardless of which hera role kind (coordinator, worker, or freelance) is being nuked, since the check reads the task's own status column, not the hera role's kind or status;
- for a task that IS in_progress (actively live) at the moment of reclaim, GUARANTEES it still reaches complete once the reclaim's own session stop (below) actually exits — even though the moment-of-archive snapshot above leaves its status untouched. Reclaim marks such a task as awaiting forced completion before backgrounding its stop; the session-exit handler that later observes that stop's exit (an unavoidably asynchronous event, since the stop itself is backgrounded to avoid blocking the UI thread on a bulk cascade) consults that mark and lands the task at complete instead of the ordinary crash/stop/fast-fail → in_review rule that governs an otherwise-identical, non-reclaim exit. This does not override a task that still holds a live worker-kind hera binding at exit time — that invariant (a task never self-completes while worker-bound) always wins if it somehow still applies;
- retains the role's inbox/messages — because the role row is retained (only stamped nuked/archived), its messages stay attached as history (no message rows are deleted, no message-archive column is required, and a nuked role's inbox stays readable);
- RECLAIMS the real resources: stops the session (backgrounded), removes the worktree + LOCAL and REMOTE branch (also backgrounded), and, once that reclaim is confirmed complete AND the task holds no live session, DELETES the argus task row (`db.PruneTasks`) — the row no longer sits archived forever; this is the one durable exception to "never hard-delete," and it applies ONLY to the argus task row, never to any hera table.

Nuking a SINGLE ROLE (not a cascade) SHALL first run the merge-safety classifier's Tier A (local-only, no network) check against that role's task, computed off the UI thread, and open the merge-safety review popup (see "Merge-safety review popup") with that one task as its sole candidate, in place of a plain confirm — choosing to clean (via either popup action, which are equivalent at a single candidate) proceeds with the nuke exactly as described above; choosing Cancel aborts it. This is a WARNING, never a gate: a not-confirmed-merged task can still be cleaned via the popup's override action.

Nuking a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept, status untouched, never pruned). The role row is marked nuked + its binding ended either way.

Nuking a COORDINATOR / orchestrator HEADER SHALL cascade the SAME mark-nuked-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are marked nuked + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone, never pruned). This cascade path does NOT use the merge-safety review popup (see "Merge-safety review popup is scoped to single-role nuke and the global Cleanup action, not cascade or clear-archived") — it keeps its existing all-or-nothing confirm, augmented with the existing confirmed/not-confirmed count (still Tier A only, per task, unchanged) AND a separate, NEW stacked-branch discovery pass (mergesafety Tier D): for every reclaimed task whose OWN Tier A check is not-safe, the system additionally checks whether it is part of a `base_branch` chain whose tip is confirmed merged (Tier A or Tier B — see "Tiered merge-safety classification"'s Tier D), reporting any such rescued branch as an extra origin branch confirmed-safe to delete alongside the task's own tracked branch.

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (including any internal-bridge worktree between two subtree orchestrators), how many tasks are preserved, how many of the reclaimed tasks are confirmed merged vs. not confirmed via their own Tier A check, and — separately — how many extra stacked branches Tier D found confirmed-safe to delete. The operator may exclude any listed extra Tier D branch from deletion; an excluded branch is recorded and never re-offered or auto-deleted by the reconciliation sweep. Unlike the existing per-task Tier A count, the Tier D discovery pass MAY make a bounded, batched GitHub network call (to confirm a stack's tip via Tier B when its local ancestry check alone cannot) — a deliberate, scoped exception to the "a nuke confirm never waits on the network" rule that continues to hold for the per-task count above, for single-role nuke, and for clear-archived, none of which perform a Tier D pass.

The difference from the `a` HIDE key: `a` HIDES (Tier 1) — the row moves into its parent coordinator's nested archive and the worktree/session stay ALIVE, fully reversible; `Ctrl+D` NUKES (Tier 2) — the row leaves the rail entirely, its worktree/session are reclaimed, and its argus task row is eventually deleted once that reclaim settles.

A reclaim whose worktree/branch removal or task-row deletion does not complete before the process exits, crashes, or is interrupted is NOT lost: a reconciliation sweep (daemon startup and periodic) re-derives the same candidate set from durable state (an ended hera binding with `end_reason=user_deleted`, worktree existence on disk, git ancestry, GitHub PR state, and any operator-excluded branch) and finishes it — retrying the worktree/branch removal and/or extra-branch deletion, then pruning the row once everything is confirmed done and the task holds no live session.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraNukeRole`, `heraReclaimAndArchiveTask`, `heraCascadeNukeFrom`, `heraDoCascadeNuke`, `heraTaskBoundOutside`), `internal/tui/app.go` (`handleSessionExitUI`, `pendingHeraReclaim`), `internal/tui/hera/ops.go` (`NukeRole`, `NukeOrchestrator`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`, `RollHeraWorkerToReview`), `internal/db/tasks.go` (`SetStatus`, `PruneTasks`), `internal/mergesafety` (Tier A/B/D classification), `internal/hera/reclaim_sweep.go` (`ReconcileHeraReclaims`), `internal/daemon/bounce.go` (`ReconcileOnStartup`), `context/knowledge/gotchas/hera-view.md`.

`NOTE:` NET zero hard deletes from any HERA table — every nuked role, orchestrator, inbox, and binding row is retained and retrievable via the DB. The argus TASK row is the one exception: it is archived immediately and, once its reclaim (worktree, branch(es), and any confirmed-safe stacked branch) is confirmed complete and it holds no live session, hard-deleted via the existing `PruneTasks` — never left archived forever.

#### Scenario: Nuking a sole-bound role removes it from the rail and reclaims its worktree

- **WHEN** a role is nuked and its task has exactly one live binding
- **THEN** the session is stopped, the worktree + local and remote branch are reclaimed, the role row is marked NUKED (invisible to the rail) with its binding ended, and the argus task row is ARCHIVED — the hera rows are never hard-deleted

#### Scenario: Nuking a multi-bound role preserves the task

- **WHEN** a role is nuked and its task holds live bindings in more than one orchestrator
- **THEN** the role row is marked nuked + its binding ended; the task is left fully alone (not archived, worktree kept, status untouched, never pruned) and its other-orchestrator binding survives

#### Scenario: Nuking a coordinator cascades over the full subtree and reclaims worktrees

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header and the operator confirms
- **THEN** that orchestrator, every nested sub-coordinator, and all their agents are marked NUKED (removed from the rail) — sessions stopped and each sole-bound task's worktree + local and remote branch reclaimed — with no hera row hard-deleted (rows retained, inboxes readable)
- **AND** a task bound live in an orchestrator outside the subtree is preserved (left fully alone, never pruned)

#### Scenario: Cascade confirm states the counts

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header
- **THEN** a confirmation modal opens stating how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (counting the internal-bridge worktree in a multi-level subtree), how many tasks are preserved, how many of the reclaimed tasks are confirmed merged vs. not confirmed via their own Tier A check, and how many extra stacked branches were found confirmed-safe by the Tier D pass

#### Scenario: A reclaimed task's row is pruned once its worktree and branches are confirmed reclaimed

- **WHEN** a nuked task's worktree and branch(es) have been confirmed removed and the task holds no live hera binding and no live session
- **THEN** the argus task row is deleted (`db.PruneTasks`), not left archived

#### Scenario: A task still live at nuke time is not pruned until its exit settles

- **WHEN** a task is `in_progress` at the moment of reclaim (its session stop is still backgrounded and has not yet exited)
- **THEN** the task row is archived as usual but NOT deleted inline; it is left for the reconciliation sweep, which only prunes it after the existing session-exit handling has landed the task at a terminal status and no live session remains

#### Scenario: A cascade nuke's confirm surfaces stacked branches confirmed safe

- **WHEN** a cascade nuke's pre-confirm merge-safety pass finds that a reclaimed task's branch is part of a `base_branch` stack whose tip is confirmed merged, and every earlier link (including this task's own branch) is confirmed a real git ancestor of that tip
- **THEN** the confirm modal lists those extra origin branches as confirmed-safe to delete, in addition to the task's own tracked branch, and the operator may exclude any of them before confirming

#### Scenario: An excluded stacked branch is never re-offered or auto-deleted

- **WHEN** the operator excludes a discovered stacked branch from deletion in the cascade confirm
- **THEN** that branch is recorded as excluded and neither the immediate nuke nor any later reconciliation sweep deletes it or re-surfaces it as a candidate

#### Scenario: A reconciliation sweep finishes an interrupted reclaim

- **WHEN** the daemon starts, or a periodic sweep tick runs, and finds a nuked task whose worktree directory still exists on disk, or an approved (non-excluded) stacked branch still exists on origin, or whose reclaim is otherwise fully complete but the row was never pruned
- **THEN** the sweep retries the worktree/branch removal and/or the extra branch deletion as needed, then prunes the row once everything is confirmed done and the task holds no live session — with no separate manual step required

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

#### Scenario: Single-role nuke and clear-archived never call the network

- **WHEN** a single-role nuke's review popup or a clear-archived sweep prepares its merge-safety check
- **THEN** it runs in a background goroutine off the UI thread, invokes no `gh`/GitHub network call, and the relevant popup/confirm only opens once the check completes — this is unchanged by the cascade-only Tier D addition below

#### Scenario: Cascade nuke's Tier D pass may wait on a bounded, batched GitHub call

- **WHEN** a cascade nuke's pre-confirm pass runs its per-task Tier A check (unchanged, no network) and its Tier D stacked-branch discovery
- **THEN** the per-task Tier A check itself still makes no network call, but the Tier D pass MAY issue bounded, batched GitHub calls (one classification per distinct stack tip, not one per branch) to confirm a stack tip that Tier A alone cannot — the confirm modal opens only once both the Tier A pass and the Tier D pass complete, a deliberate exception to the network-free rule that continues to hold for every other nuke-confirm path
