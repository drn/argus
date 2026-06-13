# Orchestration / DAG Gotchas

Stacked-PR / depends_on flow — invariants that caused bugs when violated.

## Task creation

- **DependsOn skips Step 5 in CreateAndStart, not Step 1-4.** A blocked task still creates its worktree, generates a session ID, and persists the row — only the runner.Start call is deferred. Callers that assume "blocked = nothing happened on disk" will leak worktrees when they archive without `RemoveWorktreeAndBranch`.
- **HeadlessCreateTask returns (task, nil) for blocked tasks.** The session handle is nil because no agent is running. API callers that try to attach immediately must check Status before dereferencing the daemon's `Get(taskID)` session.
- **AutoName runs on blocked tasks too.** The Haiku rename happens fire-and-forget after the row is persisted — agent liveness is not required. Removing this branch would silently break sub-task naming for stacked workflows.
- **`StartPendingBlocked` is idempotent via `HasSession` guard.** A previous call may have succeeded `runner.Start` but failed `db.Update` (SQLite write error), leaving the task Pending with a live session in the runner. The HasSession short-circuit lets the watcher's retry tick sync the DB without calling `runner.Start` a second time. Removing the guard would orphan the original process on every recovery tick.

## Depswatcher

- **depswatcher only starts pending tasks with non-empty DependsOn.** A pending task with no deps is CreateAndStart's responsibility; the watcher refuses to touch it. If you change this, scheduler-fired tasks and orphan pending rows from a failed start will spontaneously launch on the next tick.
- **Missing deps keep the task blocked forever.** When a referenced dep ID is deleted from the DB, depswatcher treats it as unresolved — refusing to start instead of falling through. The orchestrator must observe `(missing)` in `task_get` blocked_by and remediate (re-create the dep, change the depends_on, or archive the dependent).
- **Status, not result, is the gate.** depswatcher does NOT inspect `result.failed:true`. A failed dep that nevertheless reached StatusComplete unblocks dependents — the orchestrator's contract is to interpret the failure flag and intervene before downstream work cascades. Use `task_stop` for any downstream that has already started (depswatcher beat you to it) and `task_archive` for any downstream still in `pending` (`task_stop` errors with session-not-found on blocked tasks). Putting failure semantics in the watcher would couple the daemon to a payload schema it intentionally leaves opaque.
- **First tick fires immediately on Start.** Tasks that became unblocked while the daemon was down get caught up without waiting a full interval. If you change to ticker-only, restart loses up to one interval of pending work.
- **fireMu-equivalent isn't needed.** Unlike the scheduler, the watcher's start path is a CAS via the status guard inside StartPendingBlocked — two concurrent ticks racing on the same task have the loser's `runner.Start` reject because Status is no longer Pending. Adding a mutex would not improve anything.

## task_set_result

- **Re-encode via json.Marshal before persisting.** The raw bytes from the wire can include trailing whitespace, integer formatting quirks, or key ordering. Re-marshalling produces deterministic storage so test assertions and orchestrator diffs work.
- **64 KiB cap is on the serialized form.** Counting input bytes would let a pathological input expand on re-encode; counting output is the safe order.
- **Result is opaque to the daemon.** SQLite stores it as TEXT, the watcher never reads it, the MCP server renders it inside a code block. Adding any daemon-side parse of the result would couple the orchestration contract to the daemon — keep it agent-owned.

## ARGUS_TASK_ID env var

- **Skip the export when task.ID is empty.** CreateAndStart sequences db.Add → ID assignment → BuildCmd, so task.ID is non-empty in practice. The defensive `if task.ID != ""` guard ships an explicit no-export instead of `ARGUS_TASK_ID=` (empty value) if that invariant ever changes.
- **cmd.Env starts from os.Environ().** Replacing without inheriting breaks PATH, HOME, NODE_PATH, and every other env var the agent backend relies on. The append-after-os.Environ pattern is the only safe form.

## task_create idempotency

- **Only applies when name was user-supplied.** AutoName (no `name` arg) skips the duplicate check because two prompts producing the same first-40-chars slug are coincidental, not orchestrator restarts. If you flip this, schedule-fired tasks would collide with each other on the timestamp suffix.
- **Archived rows are skipped.** Archive is the user's "kill this stack" signal; re-using the slug after archive must work, otherwise the orchestrator can't retry a failed stack with the same naming scheme.

## Schema migration

- **base_branch / depends_on / result / plan_slug are idempotent ALTER ADDs.** The CREATE TABLE has them; the migration block adds them. Both paths must coexist so fresh DBs and migrated DBs end up identical. If you reorder taskColumns without updating the INSERT/UPDATE/scan strings in lockstep, the scan will mis-bind columns silently (status into worktree, etc.).

## Hera schema / store (M1)

- **FK cascade in the hera_* tables requires `foreign_keys(on)` on the connection DSN.** SQLite defaults `PRAGMA foreign_keys` OFF per-connection, and Argus's DSN historically didn't set it. Both `Open` and `OpenInMemory` now append `_pragma=foreign_keys(on)` (modernc applies it to every pooled connection). Without it, `DeleteHeraOrchestrator`/`DeleteHeraRole` would orphan child rows instead of cascading — and the failure is silent (no error, just leftover rows). Safe to enable globally because no other Argus table declares an FK.
- **Nullable timestamp columns (archived_at/pinned_at/ended_at/end_reason) must be real `NULL`, never `''`.** The partial indexes (`WHERE archived_at IS NULL`, `WHERE ended_at IS NULL`) only index rows where the column is actually NULL; storing an empty string (the convention Argus's `tasks` uses for absent times) would exclude "live"/"active" rows from the partial unique index and silently break the uniqueness backstop. This is why the hera scanners use `sql.NullString`→`*time.Time` and writes set `= NULL`, diverging from the task-side `formatTime('')` convention.
- **Multi-binding live-uniqueness is per-`(task, orchestrator)`, not per-task** (Hera migration 0004). `orchestrator_id` is denormalized onto `hera_bindings` so the partial unique indexes need no JOIN. One task can be a worker in orchestrator A and a coordinator in B simultaneously. The orchestrator-agnostic single lookups (`HeraLiveBindingByTask`, `HeraLiveBindingByWorktree`) return `ErrHeraAmbiguous` when 2+ live rows match — callers must use the `…AndOrchestrator` / `List…` variants. Role-side stays one-live-binding-per-role.
- **Task deletion ends live bindings; archive does NOT.** `tasks.go Delete` inlines an `UPDATE hera_bindings SET ended_at=… WHERE argus_task_id=… AND ended_at IS NULL` inside its existing tx (it can't call the `*DB` method `EndHeraBindingsForTask` because `WithTx` holds `d.mu` and the mutex is non-reentrant). `SetArchived` deliberately leaves bindings live because archive is resumable. There is intentionally NO FK from `hera_bindings.argus_task_id` to `tasks` — argus_task_id is plain TEXT — so this app-level hook is the only thing that severs the link on delete.
- **Role+binding creation is transactional in one tx** (`CreateHeraRoleWithBinding`), fixing Hera's deferred debt where it was two non-transactional execs. If the binding insert violates a live-uniqueness index, the role insert rolls back too — no orphan role. Born-bound spawn (M4) builds on this.
- **The partial unique indexes are the race backstop, not just the app-level pre-checks.** `CreateHeraOrchestrator`/`CreateHeraRole` pre-check for an existing active row, but two concurrent creates resolve deterministically at the index (INSERT fails) rather than corrupting state.

## Linking + DAG

- **orch.Link stages the hypothetical edge on a child *copy*, not the snapshot's pointer.** A `Store` implementation that shares pointers between `Get()` and `Tasks()` (e.g. the MCP test mock) would otherwise see the parent appended twice — once via the snapshot mutation, once via the post-cycle-check `Update`. Production `*db.DB` returns fresh scanned objects so this never bites in real code, but the defensive copy keeps tests using a sharing mock from emitting duplicate edges.
- **orch.FindCycle returns the cycle path, not a bool.** A "cycle detected" error without the offending sequence is unactionable in the linking UI. Callers (web modal, TUI banner, MCP error) render `"A → B → C → A"` directly from the returned slice.
- **HaltDownstream re-queries each row's status inside the loop.** The depswatcher can flip a `pending` row to `in_progress` between the snapshot and the per-row decision; the re-query catches that and falls back from archive→stop. Without it, a racy halt would archive a row whose agent had just started, leaving an orphan PTY.
- **HaltDownstream archives `in_review` rows, not just `pending`.** An `in_review` task has no live session (the agent already exited) — calling `stopper.Stop` on it returns session-not-found and would falsely pad `report.Stopped`. The switch folds `in_review` into the archive bucket alongside `pending` so the halt summary reflects what actually happened.
- **HaltDownstream archive path uses the narrow `SetArchived` column write, not `db.Update`.** The full-row write would clobber a concurrent depswatcher-driven `pending → in_progress` status flip. Same pattern as `Link`/`Unlink`/`SetPlanSlug`.
- **HaltDownstream does NOT halt the seed task.** The seed is the user-supplied "this milestone failed" anchor; halting it would clobber the result.failed payload the orchestrator just wrote. Only the seed's transitive descendants are stopped/archived.
- **plan_slug is opaque to the daemon — same contract as result.** The daemon never inspects it, never auto-derives it, never enforces uniqueness. The orchestrator stamps every sub-task in a stack with the same slug; the DAG view uses it as a filter key. Empty string = unaffiliated.
- **Per-task linking endpoints AND halt-downstream are open to any authenticated token** (single-tier auth — see `web-remote.md` "Per-device tokens"). Halt-downstream affects multiple rows in one call and is destructive cross-task, but carries no RCE/credential risk, so it is not on the master-only denylist (backends CRUD, self-update, token mint/revoke).
- **DAG layout assumes acyclic input.** `dagview.Compute`'s Kahn topological sort uses a small cycle guard (the in-progress visit map) to avoid infinite recursion if a stale snapshot leaks a cycle past the daemon's gate. The result is a degraded but bounded layout, not a crash — but the real fix lives upstream in the cycle DFS.
