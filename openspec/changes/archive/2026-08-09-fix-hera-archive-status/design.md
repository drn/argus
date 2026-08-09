## Context

`heraReclaimAndArchiveTask` (`internal/tui/heraactions.go`) is the single code path that ends a Hera role's real resources and archives its bound argus task: it stops the session, reclaims the worktree + branch, and calls `db.SetArchived(taskID, true)`. It never touches `task.Status`. Three callers funnel through it — `heraNukeRole` (Ctrl+D on a single role), `heraDoCascadeNuke` (Ctrl+D on a coordinator/orchestrator header, cascading over the whole subtree), and `heraNukeArchivedRole` (`C`, clearing a coordinator's Tier-1 hidden archive) — so a fix here covers every nuke/reclaim entry point uniformly, regardless of role kind (coordinator/worker/freelance), because the function operates on the task's own `Status` column, not the hera role kind.

`db.PruneCompleted` (the Ctrl+R "prune completed tasks" action) only ever looks at `status='complete'` rows (as of #927, also excluding rows with a live Hera binding). Because `heraReclaimAndArchiveTask` never advances `status`, an archived-and-reclaimed task is left wherever its status happened to be at nuke time — almost always `in_review`, since the normal worker finish path (`hera_status(status=done)` → `RollHeraWorkerToReview`, BUG-050) already rolls a finished worker's task from `in_progress` to `in_review` before a human ever nukes it. The result: the task is invisible to PruneCompleted forever. A live audit on 2026-08-09 found 737 tasks across every project in exactly this state (`archived=1`, `status=in_review`, Hera binding already ended).

The existing precedent for a Hera-lifecycle event driving a task-status transition is `db.RollHeraWorkerToReview` (`internal/db/hera.go`): it flips `in_progress → in_review` and ONLY when the task is currently `in_progress` — "never clobber a human-set state" is the load-bearing invariant, checked via an exact-status guard rather than any liveness/session check. This design mirrors that shape for the archive step: advance `in_review → complete`, gated on the task currently being `in_review`, and nothing else.

## Goals / Non-Goals

**Goals:**

- When `heraReclaimAndArchiveTask` archives a task whose current status is `in_review`, also advance it to `complete`, so it becomes visible to `PruneCompleted` — closing the actual reported gap.
- Apply the same rule regardless of which Hera role kind (coordinator/worker/freelance) is being nuked/reclaimed, since the check is purely on the task's own status column.
- Never advance a task that is still `pending` or `in_progress` at archive time — that status represents genuinely unfinished/abandoned work (e.g. an operator force-nuking a live, still-working role), and forcing `complete` would misrepresent it. This is symmetric with `RollHeraWorkerToReview`'s own "only from `in_progress`" guard.
- Idempotent: a task already `complete` when archived stays `complete` (no-op status write).

**Non-Goals:**

- No change to `heraReclaimAndArchiveTask`'s existing archive/reclaim mechanics (session stop, worktree+branch removal, `db.SetArchived`) — only a new status check is added alongside them.
- No retroactive backfill of the 737 already-stranded tasks. That's explicitly a separate follow-on stage (a historical sweep) that depends on this change landing first; scoping it in here would conflate "stop the bleeding" with "clean up the past," and the sweep needs its own design (bulk-operation safety, confirmation UX, etc.).
- No new "failed" task-status value. A task rolled to `in_review` via `RollHeraWorkerFailed` (no `ready_to_close` stamp) still advances to `complete` on archive under this rule — `complete` here means "no longer needs attention," not "succeeded"; pass/fail outcome is tracked separately via `TaskResult`'s opaque `{"failed":true}` blob, orthogonal to the coarse status workflow (see `coordTaskFailed` in `internal/tui/hera/details.go` for the existing precedent of reading outcome from `TaskResult`, not `Status`).
- No change to `PruneCompleted` itself — it already does the right thing once `status='complete'` is reachable.

## Decisions

**Decision: gate on `task.Status == StatusInReview` at the moment of archive, not on hera role status or session liveness.**

Alternatives considered:

- *Gate on the hera role's own status (`done`/`failed`) instead of the task's `Status` column.* Rejected: coordinator and freelance roles don't go through `RollHeraWorkerToReview` at all (it's worker-kind-only, gated by `TaskHoldsLiveHeraWorkerBinding`), so a role-status check would silently exclude two of the three role kinds even though the mission is explicit that the rule should reason uniformly across kinds. The task's `Status` column is the one signal common to all three.
- *Gate on session liveness (only complete a task whose session is already dead).* Rejected: `heraReclaimAndArchiveTask` unconditionally stops the session as part of reclaim (backgrounded via `heraGoSafe`), so by the time reclaim finishes every nuked task's session is being torn down regardless — liveness at call-time isn't a meaningful signal here. What *is* meaningful is whether the task had already reached its natural resting/review state before the operator chose to nuke it. A task can legitimately be `in_review` with a still-alive (idle) session — that's the documented BUG-050 shape ("Claude workers finish their report and go idle rather than exiting") — and such a task genuinely IS done; excluding it on a liveness check would just reintroduce the bug for the common case.
- *Advance any non-`complete`, non-`pending` status (i.e. `in_progress` OR `in_review`) to `complete` on archive.* Rejected: this is the case the mission explicitly warns against — a still-`in_progress` task being nuked (operator force-killing a live, still-working role, or a whole-subtree cascade catching an actively-running coordinator) would falsely read as "completed" rather than "abandoned mid-flight." Excluding `in_progress` preserves that distinction, exactly mirroring `RollHeraWorkerToReview`'s refusal to fire outside its one specific source status.

**Decision: reuse `db.SetStatus` (the existing generic status-transition primitive) rather than adding a new Hera-specific DB helper.**

`SetStatus` already handles the derived `ended_at` timestamp and emits `task.status_changed`/`task.completed` events identically to every other status-transition call site (`RollHeraWorkerToReview` included) — this is plain `pending → in_progress → in_review → complete` state-machine advancement (see `openspec/specs/task-lifecycle/spec.md`, "Status transition timestamps"), not a Hera-specific concern, so it doesn't need Hera-specific machinery. It's already part of the generic `store.Store` interface `heraReclaimAndArchiveTask` already calls through (`a.db.SetArchived` is called the same way), so no type assertion or new interface method is needed.

**Decision: this requirement belongs to the `hera-view` capability, not `hera-coordination` or `worktree-management`.**

`openspec/specs/hera-view/spec.md`'s "Conservative delete semantics for multi-binding safety (area 7)" is the existing requirement that fully documents `heraReclaimAndArchiveTask`'s exact contract (derived-from line names the function directly). `hera-coordination` owns the MCP tool surface and the hera store's own state machine (roles/orchestrators/bindings) — `heraReclaimAndArchiveTask` is TUI-side application code, not part of that surface. `worktree-management` owns worktree/branch lifecycle and already has its own "Pruning completed tasks" requirement (from #927) that this change doesn't touch — it's a downstream consumer of the status this change fixes, not the owner of the archive-time transition itself.

## Risks / Trade-offs

- **[Risk]** A task legitimately re-opened after reaching `in_review` (e.g. a coordinator or human intentionally continues work post-review) that then gets nuked while still showing `in_review` would be marked `complete` even though "more work was planned." → **Mitigation**: this is already how the codebase treats `in_review`: `ReviveHeraWorkerToInProgress` (`db/hera.go`) treats a re-engaging worker's `in_review` task as resumable (rolling it back to `in_progress`) unless it's `heraWorkerAwaitingCloseout` — but the moment an operator deliberately nukes/reclaims the role (a `Ctrl+D`, always behind a confirm), that's an explicit human decision to end the task's Hera lifecycle. Treating that as "no longer needs review" is consistent with what nuke already means for every other piece of state it touches (worktree gone, branch gone, session stopped).
- **[Risk]** `heraDoCascadeNuke` iterates every role in a subtree, including coordinators; a coordinator's own task is almost never `in_review` in practice (`RollHeraWorkerToReview` is worker-kind only), so cascade-nuking a coordinator will usually leave its task status untouched (still `in_progress`) even though the whole orchestrator is being torn down. → **Mitigation**: explicitly a Non-Goal here (see above) — accepted as pre-existing, out of scope; forcing coordinator tasks to `complete` unconditionally would violate the "never clobber active work" guard for exactly the case the mission calls out (a live/still-running session getting archived). A separate, explicitly-scoped follow-on could address coordinator-specific close-out semantics if it becomes a problem in practice.
- **[Risk]** Idempotency / double-write: `heraDoCascadeNuke`'s `reclaimed` map already prevents `heraReclaimAndArchiveTask` from being called twice for the same task within one cascade, so no double status-transition risk there.

## Open Questions

None — the mission's three explicit design questions (uniform across role kinds? what does "genuinely finished" mean? exclude live/still-running sessions?) are resolved above via the `Status == in_review` gate.
