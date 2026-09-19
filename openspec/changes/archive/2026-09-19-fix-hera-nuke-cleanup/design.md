## Context

Investigation (direct queries against the live `~/.argus/data.sql` DB, plus
reading the actual nuke code paths) confirmed three distinct gaps, all the
same underlying shape: `Ctrl+D` (nuke) promises to fully clean something up,
but the real-world side effect is dispatched into a fire-and-forget
background goroutine with no durability, so it silently doesn't always
finish:

1. **Worktree/branch leak.** `heraReclaimAndArchiveTask`
   (`internal/tui/heraactions.go`) backgrounds `git worktree remove` +
   branch deletion via `heraGoSafe`. If that goroutine loses the race
   against process exit/restart/crash, the worktree is orphaned forever —
   no error, no record. Proven: 13 real git worktrees still fully
   registered in `git worktree list` weeks after their role + orchestrator
   were nuked. The one existing safety net — the orphan-worktree sweep
   inside the `Ctrl+R` prune flow — can never catch these:
   `DB.WorktreePaths()` (`internal/db/tasks.go`) returns every task's
   `worktree` column regardless of archived state, so a leaked path always
   looks "still owned."

2. **Task-row pile-up.** Nuke always archives the task row
   (`db.SetArchived`) but never deletes it — verified across every one of
   268 nuked orchestrators in the live DB, 0 exceptions. Deletion only ever
   happens via a fully separate, always-manual action (`Ctrl+R` /
   the `c` Cleanup popup) in the *Tasks* tab. The operator works almost
   entirely in Hera, where nuked rows are invisible by design — so the
   Tasks-tab archive silently grew to 300+ rows with nothing surfacing it.

3. **Stacked-branch cleanup.** A hera coordinator that spawns a chain of
   workers, each branched off the previous (`base_branch` stacking), and
   squash-merges the tip's PR, leaves every earlier branch in the chain
   permanently unconfirmable by the existing merge-safety tiers: a plain
   ancestry check against `main` fails post-squash, and only the tip ever
   gets its own PR. The operator currently runs a manual `/cleanup` skill
   (`~/.claude/skills/cleanup/SKILL.md`) before every `Ctrl+D` specifically
   to sweep these by hand — a step that's easy to forget and structurally
   can't be triggered by argus itself today.

The current locked spec (`openspec/specs/hera-view/spec.md`) explicitly
documents nuke's task-row handling as "ARCHIVES … never `db.Delete`," so
part of this design is a deliberate, spec-level behavior change, not a
bugfix within existing behavior.

## Goals / Non-Goals

**Goals:**
- Make nuke's worktree + branch reclaim durable — no goroutine loss should
  ever leave a permanent leak.
- Once reclaim is confirmed complete and the task has no live session, the
  task row should be deleted, not archived forever.
- Detect and (with confirmation) clean up stacked-branch chains whose tip is
  confirmed merged, folding the operator's manual `/cleanup` step into
  `Ctrl+D`.
- Self-heal the existing 13 known-leaked worktrees the first time this
  ships, with no separate manual cleanup step.
- Never delete anything from `origin` without the operator seeing the list
  first — this bar does not loosen just because detection is now automatic.

**Non-Goals:**
- Not attempting full parity with `/cleanup`'s "scan this conversation's own
  history" signal (sub-agent `isolation:"worktree"` calls) — that requires
  LLM/session memory the Go daemon/TUI has no access to. This design only
  automates the git-state-derivable portion (stacked `base_branch` chains,
  ahead/merged/PR-state checks).
- Not changing the bedrock "hera role/orchestrator/binding rows are never
  hard-deleted" invariant — only the argus *task* row's terminal fate
  changes.
- Not adding a single-role-nuke merge-safety-popup equivalent for the
  stack-walk — Tier D is scoped to the cascade-nuke path, matching where
  the stacked-worker pattern actually occurs (a whole coordinator subtree),
  mirroring the existing single-vs-cascade split already in `hera-view`.

## Decisions

### D1: Re-derive from durable facts, not a new "pending" flag

Rejected: a new `task_meta` "reclaim_pending" marker set before backgrounding
work, cleared after. Alternative chosen: the reconciliation sweep re-derives
everything it needs from data that already durably exists — worktree
existence on disk, `hera_bindings.end_reason`, git ancestry, GitHub PR
state — rather than trusting a marker that could itself fail to be written
durably before a crash. This mirrors how the fix is *found* (diffing disk
against DB) rather than adding a new failure-prone bookkeeping layer.
Exception (D4): the operator's explicit "don't delete this branch" choice
*is* new information with no other durable home, so it gets a minimal
`task_meta` entry — everything else is re-derivable.

### D2: Prune reuses `PruneTasks`, not new delete logic

`DB.PruneTasks([]string{taskID})` (`internal/db/tasks.go`) already
re-verifies the live-binding guard at delete time — exactly the one
invariant that must never be skipped. The nuke path calls this directly
rather than growing its own delete path, so `Ctrl+R`, the Cleanup popup, and
nuke's own auto-prune all share one safety-checked implementation.

### D3: Inline prune only when there's no live session; the sweep covers the rest

`handleSessionExitUI` (`internal/tui/app.go`) starts with
`t, err := a.db.Get(taskID); if err != nil || t == nil { return }` — a task
still `in_progress` at nuke time arms `markHeraReclaimPending` and is later
forced to `complete` by that handler once its backgrounded stop's exit is
observed. Pruning the row before that settles would make the handler
silently no-op, orphaning the in-memory marker and breaking the existing
"a reclaimed live task still reaches complete" guarantee. Decision: the
inline prune-on-reclaim path only fires when `!a.runner.HasSession(taskID)`.
A still-live-session task is left entirely to the periodic/startup sweep,
which by construction only ever runs after the session has actually exited
and the task has settled — at which point it's exactly as safe to prune as
any other case. No change to `handleSessionExitUI` itself.

### D4: Tier D walks `base_branch`, checks ancestry against the tip (not `main`)

Alternative rejected: teach Tier B to also try `git merge-base
--is-ancestor <branch> main` as a fallback when the PR-state check misses.
This is unsound — it's exactly what fails after a squash merge, since the
squashed commit on `main` is a different object than anything in the
branch's own history. Chosen instead: classify the *tip* of the stack via
existing Tier A/B (PR `state == MERGED`, immune to merge strategy), then
walk `base_branch` backward (`model.Task.BaseBranch`, already tracked —
reverse-lookup `Branch == BaseBranch` at each step) and check each earlier
link's ancestry against the **tip's own branch**, not `main`. The tip's
branch still holds the real, un-squashed commits from every earlier link,
so this check is sound and — critically — fails closed (reports "not safe")
if anything mid-stack was rebased or cherry-picked instead of a straight
graft, rather than false-negatively "confirming" something it can't
actually verify.

This is a direct generalization of the existing Tier C
(`runCoordinatorInferencePass`, `internal/api/cleanup_candidates.go`), which
already infers a worker's safety from its coordinator's confirmed-merged
branch for the structurally identical "can never get its own PR" case. Tier
D is the same trick walked along a different edge (`base_branch` instead of
orchestrator→coordinator).

### D5: Extra-branch deletion folds into the existing cascade confirm, not a second prompt

`heraCascadeNukeFrom` already runs a Tier-A pass off the UI thread before
opening its confirm modal. Tier D's stack-walk runs in that same pass; the
confirm modal's message is extended to list any extra origin branches found
confirmed-safe, so approval happens in the same interaction as the nuke
itself — matching `/cleanup`'s own bar (never delete an origin branch
without the operator seeing the list) while removing the separate manual
step entirely. Branches the operator excludes are recorded (D1 exception)
so the sweep doesn't re-discover and delete them later.

### D6: One reconciliation sweep, three jobs, wired like the existing precedent

`internal/hera/reclaim_sweep.go`, sibling to the existing
`heraadopt.ReconcileBindings` (`internal/hera/adopt.go`) — same idempotent,
safe-to-rerun contract, wired at the same two points
(`Daemon.ReconcileOnStartup` in `internal/daemon/bounce.go`, plus a new
periodic ticker in `internal/daemon/daemon.go` next to the existing
`runPRPoller`/`runUsageBudgetPoller`). One function handles all three
unfinished-cleanup shapes (worktree still on disk, approved extra branch
still on origin, fully-reclaimed-but-never-pruned row) because they share
the same candidate set (tasks with a `user_deleted`-reason hera binding) and
the same terminal action (prune once everything above is confirmed done).

## Risks / Trade-offs

- **[Risk]** Auto-deleting the task row is irreversible (no more "recover by
  re-spinning a fresh worktree" window that today's archive-forever
  behavior gives an accidental nuke). → **Mitigation**: this is the
  operator's explicit, informed choice (discussed and chosen over a
  delayed-sweep alternative that would have kept a recovery window); the
  existing cascade confirm modal is the speed bump, unchanged.
- **[Risk]** Tier D could be fooled by a stack that *looks* linear via
  `base_branch` but was actually reconstructed differently (e.g., a branch
  name reused after deletion, pointing `BaseBranch` at an unrelated task).
  → **Mitigation**: the ancestry check (`git merge-base --is-ancestor`) is
  the actual safety gate, not the `base_branch` chain alone — a
  reused/misleading name that isn't a real ancestor of the confirmed-merged
  tip fails closed.
- **[Risk]** Deleting extra origin branches still touches shared state (a
  colleague could theoretically be working off one, though these are
  argus-spawned worker branches by convention). → **Mitigation**: unchanged
  from `/cleanup`'s own bar — always surfaced for explicit confirmation,
  never silent, same open-PR flag-and-skip behavior Tier D inherits from
  Tier A/B.
- **[Trade-off]** The periodic sweep adds a new daemon-side ticker/goroutine
  and DB query surface. → Accepted: mirrors existing, already-shipped
  pollers (`runPRPoller`, `runUsageBudgetPoller`) exactly, so it's a known,
  low-risk pattern in this codebase, not a new architectural shape.

## Migration Plan

No schema migration expected: `task_meta` already exists as a generic
per-task key/value sidecar (used today for `hera.ready_to_close`,
`hera.role`, etc.) and is the natural home for the small excluded-branch
record. No backwards-compatibility shims needed (single-user, breaking
changes are acceptable per this repo's stated policy) — on first deploy,
`ReconcileOnStartup`'s new sweep pass immediately finds and cleans up the
13 already-known leaked worktrees as a side effect of normal startup, no
separate one-off migration script required.

Rollback: revert the daemon/TUI binary. No data was migrated in place (the
sweep only deletes rows/branches/worktrees that were already fully
reclaimable under the OLD invariants too — it doesn't change what's safe,
only who's responsible for finishing the job), so a rollback loses only the
new auto-prune/auto-sweep behavior, not any prior state.

## Open Questions

- Exact periodic sweep interval (a few minutes, matching the existing
  pollers' order of magnitude) — pin down during implementation, not a
  design-level decision.
- Whether the cascade confirm's extra-branch list needs per-branch
  checkboxes (approve/exclude individually) or a single all-or-nothing
  toggle for v1 — leaning per-branch (matches `/cleanup`'s own UX), to be
  confirmed against `internal/tui/modal`'s existing confirm-modal
  capabilities during implementation.
