**Design doc:** `openspec/changes/add-merge-safety-review/design.md`
**Depends on:** `add-merge-safety-classifier` landing first.

## 1. Tests — worktree-management (PruneTasks generalization)

- [ ] 1.1 `internal/db` test: `PruneTasks(ids)` deletes exactly the given IDs (minus any failing the live-binding guard), regardless of their `status`.
- [ ] 1.2 `internal/db` test: `PruneTasks` re-verifies the live-Hera-binding guard at call time — an ID that gained a live binding after the caller's own check is skipped, not deleted.
- [ ] 1.3 `internal/db` test: `PruneCompleted()` (the existing all-`status=complete` sweep) is behaviorally unchanged — same guard, same skipped-count semantics — now expressed as a thin wrapper over `PruneTasks`.
- [ ] 1.4 `internal/agent` test: `PrunePrepare` with `opts.TaskIDs` set sources its task list from the explicit set, not the all-complete query; `PrunePlan.Run`'s slow phase is unchanged either way.
- [ ] 1.5 `internal/agent` test: existing `Ctrl+R` (`opts.TaskIDs` unset) test coverage still passes unmodified, proving zero regression.

## 2. Implementation — worktree-management

**Depends on:** Stage 1

- [ ] 2.1 Add `DB.PruneTasks(ids []string) (pruned []*model.Task, skipped int, err error)` to `internal/db/tasks.go`, re-expressing `PruneCompleted()` in terms of it.
- [ ] 2.2 Add `TaskIDs []string` to `agent.PruneOptions`; branch `PrunePrepare` to call `database.PruneTasks(opts.TaskIDs)` when set, else the existing `database.PruneCompleted()`.
- [ ] 2.3 Run `make test-pkg PKG=./internal/db/` and `make test-pkg PKG=./internal/agent/`, confirm Stage 1 passes and no existing test regresses.

## 3. Tests — merge-safety review popup + entry points

- [ ] 3.1 `internal/tui` test: single-role nuke opens the review popup (not a plain `ConfirmModal`) with the one task as its candidate.
- [ ] 3.2 `internal/tui` test: popup renders NOT-SAFE before SAFE; `Clean safe` is the default-selected action.
- [ ] 3.3 `internal/tui` test: `Clean safe` at n=1 with a SAFE candidate proceeds with the nuke; with a NOT-SAFE candidate, it's a no-op (nuke does not proceed).
- [ ] 3.4 `internal/tui` test: `Clean all` at n=1 proceeds with the nuke regardless of the candidate's verdict.
- [ ] 3.5 `internal/tui` test: `Cancel` performs no nuke.
- [ ] 3.6 `internal/tui` test: cascade nuke and clear-archived (`C`) do NOT open the popup — they retain their existing confirm, now augmented with a confirmed/not-confirmed count (Tier A, off the UI thread, computed before the confirm opens).
- [ ] 3.7 `internal/tui` test: no `gh`/network call is made from any nuke entry point (single-role or cascade) — assert via the classifier's test seam recording zero Tier B invocations.
- [ ] 3.8 `internal/daemon`/`internal/api` test: `POST /api/maintenance/cleanup-candidates/compute` starts a background pass, is idempotent while running.
- [ ] 3.9 `internal/api` test: `GET /api/maintenance/cleanup-candidates` returns cached verdicts + `computing` flag.
- [ ] 3.10 `internal/api` test: repo resolution for a global-Cleanup candidate uses the `projects` table's configured path, keyed by the task's project; a task whose project row no longer exists classifies as not-safe with an "unresolvable repo/project" reason.
- [ ] 3.11 `internal/api` test: the clean endpoint rejects device/scope tokens with 403; accepts master.
- [ ] 3.12 `internal/api` test: `scope: "safe"` deletes only confirmed-safe tasks via `PruneTasks`; `scope: "all"` deletes every cached candidate.
- [ ] 3.13 `internal/api` test: clean re-verifies the stuck-task predicate and live-binding guard per task and skips (does not error on) one that no longer qualifies.
- [ ] 3.14 `internal/api` test: clean acts on the last-computed cached snapshot, not a fresh live classification.
- [ ] 3.15 `internal/tui` test (SimulationScreen smoke): the global Cleanup action opens via the Ctrl+K palette, renders sectioned rows, and both Clean actions dispatch the expected `clean` call with the right scope.
- [ ] 3.16 Confirm every scenario in `specs/hera-view/spec.md`, `specs/rest-api/spec.md`, and `specs/worktree-management/spec.md` for this change has a corresponding failing test before implementation (Prove-It Pattern).

## 4. Implementation — popup widget + entry points

**Depends on:** Stage 3

- [ ] 4.1 Build the merge-safety review popup widget (`internal/tui`), modeled on `TaskSwitcherModal`'s grouped/sectioned rendering (header rows + scrollable item rows), with the NOT-SAFE/SAFE section order and the three-action bar (`Clean safe` default-selected, `Clean all`, `Cancel`).
- [ ] 4.2 Wire single-role nuke: in `heraOpenDelete`'s role branch, dispatch a goroutine running the classifier's Tier A check for the task, then `QueueUpdateDraw` to open the popup with the one-candidate list; wire the popup's Clean actions to the existing `heraNukeRole` mechanics (unchanged), and Cancel to a no-op close. Include the staleness guard (selection changed/vanished during the async check → don't open).
- [ ] 4.3 Extend `heraCascadeNukeFrom` and `heraClearArchive` to run Tier A checks concurrently (bounded worker pool) across their subtree's reclaimed tasks and fold a confirmed/not-confirmed count into their existing confirm message — no popup, no mechanics change.
- [ ] 4.4 Implement the daemon-side cleanup-candidate computation: stuck-task-predicate query, `task_meta` caching (tier/verdict/reason/timestamp), repo resolution via the `projects` table keyed by task project (graceful "unresolvable" classification when the project row is gone), grouped-by-repo Tier A+B classification via `internal/mergesafety`'s batch entry point, and in-flight-pass tracking.
- [ ] 4.5 Add `POST /api/maintenance/cleanup-candidates/compute`, `GET /api/maintenance/cleanup-candidates`, and `POST /api/maintenance/cleanup-candidates/clean` to `internal/api` (routes.go + handlers), with `clean` gated by `requireMaster` and implemented via `agent.PrunePrepare(database, agent.PruneOptions{TaskIDs: ...})` + `.Run(nil)` for the chosen scope.
- [ ] 4.6 Wire the global Cleanup action into the Ctrl+K command-palette registry, opening the popup with the full backlog (triggering compute + polling until ready, showing a scanning state).
- [ ] 4.7 Run `make test-pkg` for each touched package and confirm all Stage 3 tests pass.

## 5. Verification

**Depends on:** Stage 4

- [ ] 5.1 Run `make pre-pr` and confirm it passes clean.
- [ ] 5.2 `openspec validate add-merge-safety-review --strict` passes.
- [ ] 5.3 Archive this change into `openspec/specs/hera-view/spec.md`, `openspec/specs/rest-api/spec.md`, and `openspec/specs/worktree-management/spec.md` in the same PR, before merge.
- [ ] 5.4 Add a gotcha note to `context/knowledge/gotchas/hera-view.md` documenting: the popup's two entry points and their differing Tier A/A+B scope, the cascade/clear-archived boundary (no popup, count-only), and the `PruneTasks` generalization backing immediate cleanup.
- [ ] 5.5 Confirm the README's Reference appendix (REST endpoints table, keybindings if a literal binding is added) is updated for the new `/api/maintenance/cleanup-candidates*` endpoints and the Ctrl+K palette entry.
