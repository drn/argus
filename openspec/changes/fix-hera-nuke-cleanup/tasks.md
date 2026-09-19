## 1. Merge-safety Tier D (stack-inferred safety)

- [x] 1.1 Add `TierStackInferred = "stack-inferred"` constant to `internal/mergesafety/classify.go`, alongside the existing `TierLocalAncestor`/`TierMergedPR`/`TierCoordinatorInferred`.
- [x] 1.2 Add a `ResolveStackTip` (or similarly named) helper that, given a task and a lookup of `Branch -> *model.Task` for the relevant scope, walks forward via `BaseBranch` (`Y.BaseBranch == X.Branch`) to the chain's terminal task, guarding against a cycle.
- [x] 1.3 Add a `ClassifyStackInferred` function: resolves the tip via 1.2, classifies it via the existing `Classify` (allowing Tier B), then verifies each earlier link's local git ancestry against the tip's own branch (`git merge-base --is-ancestor`, reuse `gitutil`). Fails closed on any unresolved/broken link.
- [x] 1.4 Table tests in `internal/mergesafety`: clean linear stack (rescued), squash-merged tip (rescued via Tier B), broken ancestry mid-stack (not rescued), unconfirmed tip (not rescued), cycle guard.

## 2. Task-row auto-prune on nuke

- [ ] 2.1 In `internal/tui/heraactions.go`'s `heraReclaimAndArchiveTask`, after the backgrounded `agent.RemoveWorktreeAndBranch` call returns, add: if no live hera binding for the task (already guaranteed by this point) AND `!a.runner.HasSession(taskID)`, call `a.db.(*db.DB).PruneTasks([]string{taskID})` and log the outcome.
- [ ] 2.2 Confirm (via a targeted test, not just reasoning) that a task armed via `markHeraReclaimPending` at nuke time is NEVER pruned by 2.1 — only once `handleSessionExitUI` has consumed the marker and the session is no longer live.
- [ ] 2.3 `internal/tui/heraactions_test.go`: nuking a sole-bound, non-live role results in the task row being deleted (`db.Get` returns `ErrTaskNotFound`), not merely archived. Nuking an `in_progress` role archives but leaves the row present.

## 3. Reconciliation sweep

- [ ] 3.1 New file `internal/hera/reclaim_sweep.go`: add the DB query for the candidate set (tasks with a `hera_bindings` row where `end_reason='user_deleted'` and no live binding) — either a new `DB` method or inline SQL following the existing `StuckTaskCandidates` style.
- [ ] 3.2 Implement `ReconcileHeraReclaims(db *db.DB, runner SessionChecker) (Summary, error)`: for each candidate, retry worktree removal if the directory still exists, retry any non-excluded confirmed-safe stacked branch's remote deletion if it still exists on origin, then prune once both are settled and no live session remains for the task; otherwise leave it for the next run.
- [ ] 3.3 `internal/hera`: table tests (`t.TempDir()` + `t.Setenv("HOME", ...)`) — leaked worktree removed+pruned; already-reclaimed-just-prunes; excluded branch stays; live-binding skip; live-session skip (retries worktree only, does not prune).

## 4. Excluded-branch bookkeeping

- [ ] 4.1 Define the `task_meta` namespace/key for an operator-excluded stacked branch (e.g. `cleanup.excluded_branches`, following the existing `task_meta` sidecar pattern used for `hera.ready_to_close`/`hera.role`).
- [ ] 4.2 Wire the cascade confirm's per-branch exclude action (see task 5) to write this entry; wire the reconciliation sweep (3.2) to read and skip any branch recorded there.

## 5. Cascade-nuke confirm: stacked-branch discovery + deletion

- [ ] 5.1 In `internal/tui/mergesafety.go`, add a new function (do NOT modify `classifyNukeCandidate` — single-role nuke and clear-archived stay Tier-A-only, unchanged) that runs after the existing `classifyTasksConcurrently` pass in `heraCascadeNukeFrom`: for every task that classified not-safe, attempt `mergesafety.ClassifyStackInferred` (task 1.3), scoped to the tasks in the current subtree/reclaim set plus their `base_branch` ancestors resolvable from the DB.
- [ ] 5.2 Extend the cascade confirm modal's message (in `heraCascadeNukeFrom`) to list any extra origin branches Tier D found confirmed-safe, alongside the existing counts, with a way for the operator to exclude specific ones before confirming (checkbox-per-branch if `internal/tui/modal` supports it; otherwise a single all-or-nothing toggle for v1 — decide during implementation per design.md's Open Questions).
- [ ] 5.3 In `heraDoCascadeNuke`, background the confirmed (non-excluded) extra branches' remote deletion alongside the existing per-task worktree/branch reclaim (reuse `agent.DeleteRemoteBranch` locally, or `iris_branch_delete_remote` when the daemon itself runs inside an argus sandbox — mirror `~/.claude/skills/cleanup/SKILL.md`'s existing routing decision).
- [ ] 5.4 `internal/tui/heraactions_test.go` / `internal/tui/mergesafety_test.go`: a cascade nuke over a synthetic 3-task `base_branch` stack surfaces the two earlier links as extra confirmed-safe branches; excluding one persists via 4.1 and is never re-offered.

## 6. Daemon wiring

- [ ] 6.1 Call `hera.ReconcileHeraReclaims` from `Daemon.ReconcileOnStartup` (`internal/daemon/bounce.go`), right alongside the existing `heraadopt.ReconcileBindings(d.db)` call — same idempotent, log-count-on-success style.
- [ ] 6.2 Add a periodic `d.runHeraReclaimSweeper()` ticker in `internal/daemon/daemon.go`, started next to `go d.runPRPoller()` / `go d.runUsageBudgetPoller()` (~line 1188-1244); pick an interval matching the existing pollers' order of magnitude.
- [ ] 6.3 Daemon-level test: seed a synthetic leaked worktree (task archived, `user_deleted` binding, worktree dir present in a `t.TempDir()` root) and confirm `ReconcileOnStartup` cleans it up end-to-end.

## 7. Spec archiving and verification

- [ ] 7.1 `make pre-pr` green (build, vet, fmt-check, lint-pr, vuln, test-cover-gate) on the full diff.
- [ ] 7.2 `make test-cover` on every touched package; confirm ≥95% on touched lines (90% acceptable for UI-smoke-only code per this repo's testing.md).
- [ ] 7.3 Live repro: create a throwaway hera coordinator with a stacked worker chain, squash-merge the tip's PR, cascade-nuke the coordinator, and confirm via `sqlite3`/`git worktree list`/`git branch -r` that every task row, worktree, and origin branch in the stack is gone.
- [ ] 7.4 Confirm the 13 pre-existing leaked worktrees found during investigation (3 ARGUS, 9 RAI-Brain, 1 Sketch) are swept up once the daemon restarts with the new code.
- [ ] 7.5 Update `context/knowledge/gotchas/hera-view.md` and `context/knowledge/gotchas/worktree.md` (or `misc.md`) with the non-obvious invariants this change introduces (durable-vs-fire-and-forget reclaim, Tier D's cascade-only network exception, `task_meta` exclude-list), per this repo's documentation requirements.
- [ ] 7.6 `openspec archive fix-hera-nuke-cleanup` — merge deltas into base specs and move the change folder to `openspec/changes/archive/`, on the same branch/PR as the code (this repo requires archiving atomically with the merge, never as a separate post-merge step).
