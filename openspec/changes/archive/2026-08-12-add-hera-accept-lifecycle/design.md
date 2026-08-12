## Context

Hera's existing close-out primitive, `RollHeraWorkerToReview` (`internal/db/hera.go`), rolls a worker's task from `in_progress` to `in_review` the instant it self-reports `hera_status(done)`. Its own doc comment states plainly that it "never auto-completes" – that is a worker-side signal only ("I need review"), never a completion decision. `complete` has never had a corresponding, reachable trigger anywhere in the system: not the TUI, not the web frontend (a `POST /api/tasks/:id/status` endpoint accepts `"complete"` but nothing calls it), not any MCP tool. The one exception, discovered and fixed earlier the same night as this change (`fix-nuke-completion-race`), is the nuke/reclaim teardown path – which is an end-of-life action, not a normal accept-and-move-on step.

Aaron's own framing of the intended protocol: the agent signals "I need a review of my work" (existing `hera_status(done)`), the coordinator "accepts" that work (new), which tells the agent "if you have no other tasks to complete, mark yourself as done" (a message, not a forced kill). Completion CAN free memory by detaching the agent's session, but that is a nice-to-have, not a requirement of completion itself, and must be revocable.

## Goals / Non-Goals

**Goals:**

- Give the coordinator an explicit, first-class "accept this work" action (`hera_accept`) that flips the bound task to `complete` and checks in with the agent, asking it to confirm whether it's winding down, has more work, or needs to ask a question – never a forced stop.
- Auto-fire the same accept-equivalent when the coordinator's own plan-DAG rolls forward past a node's blockers – that IS the coordinator moving on, whether or not the coordinator ever calls `hera_accept` by hand.
- Make completion revocable: an accepted task, if its session is later stopped, can still be revived back to `in_progress`.
- Let the operator's existing `a` (HIDE) key additionally free the completed agent's memory, as an operator-driven, reversible-in-spirit action – never automatic, never forced by acceptance itself.
- Surface a merged PR as a coordinator-facing nudge only – never an automatic accept/complete, given how ambiguous "confirmed merged" is for Hera-descended tasks (squash merges, folded-in workers with no PR of their own – documented the same night in the merge-safety classifier work).

**Non-Goals:**

- No change to `RollHeraWorkerToReview`, `RollHeraWorkerFailed`, or the existing `in_review` semantics – the worker-reports-done path is untouched.
- No automatic session stop from `hera_accept` or the gater's auto-fire – completion and detachment are deliberately separate concerns.
- No automatic completion from a PR merge – nudge only.
- No change to task deletion/pruning, the merge-safety classifier, or the nuke/reclaim path.

## Decisions

**Decision: one shared primitive, `internal/hera.AcceptRole`, called by both `hera_accept` and the gater – never two copies of the same SetStatus-plus-send logic.**

Mirrors the existing `ReviveRole`/`RecycleCoord` shape in the same package: a narrow `AcceptStore` interface (`HeraLiveBindingByRole`, `Get`, `SetStatus`) plus an `AcceptSender` interface matching `(*hera.Service).Send`'s signature, both satisfied structurally by the real types already in use at each call site (`mcp.HeraStore`/`*hera.Service` for the MCP tool, `*db.DB`/a `hera.New(...)`-constructed service for the gater, mirroring how `daemon.go` already builds `gaterSvc := hera.New(d.db, d.notifier)` ad hoc for the existing hold-ping wiring). `AcceptRole` resolves the target role's live binding → task, no-ops cleanly when the task is already `complete` (both the status flip AND the notification are skipped – see the idempotency decision below), else calls `db.SetStatus(taskID, model.StatusComplete)` directly (it already handles idempotency and event emission) and sends the acceptance message.

**Decision: the gater fires the accept-equivalent for every blocker of a node AFTER that node has successfully materialized, sourced from the SAME `blockerIDs` already fetched for the fan-in notice – not a new query, not a new trigger point.**

`materializeNode` already resolves `blockerIDs` before deciding fan-in-notice-or-not; every one of those blockers has, by definition, just had its `done` signal consumed by the DAG moving forward – this IS Aaron's "coordinator rolls forward to the next item" trigger, with zero new state to track. The accept-equivalent is scoped to the ordinary worker-kind materialize path only (mirrors `pingFanIn`'s own existing worker-only scope note) – a `subcoord` node's blockers get the same treatment through this same call site, since `acceptBlockers` runs before the `NodeKind` branch's early return... actually it runs AFTER, so a subcoord materialization does not (yet) auto-accept its blockers; this mirrors the fan-in notice's existing scope and is called out explicitly rather than silently extended, since subcoord materialization has its own, structurally different completion story (a sub-coordinator's own subtree, not a leaf worker) that is out of scope here.

**Decision: idempotency is "second+ call against an already-complete task is a full no-op – status untouched, no message sent" – not merely "the status flip is idempotent."**

A blocker with multiple dependent nodes would otherwise get the reply-required check-in notification once per dependent that materializes – spam that grows with fan-out (and repeated, contradictory demands for a reply). `AcceptRole` checks the task's current status BEFORE flipping and returns immediately (no error) when it is already `complete`, so only the FIRST accept (whichever dependent materializes first) produces a status flip and a message; every subsequent one is silent.

**Decision (refinement, post-implementation): the acceptance message is a closed-loop check-in that demands a reply, not a one-way FYI.**

Aaron's follow-up refinement: the default message must explicitly instruct the recipient to reply with exactly one of (1) confirming it has no other tasks and is winding down, (2) telling the coordinator it still has more work to do, or (3) a question if it isn't sure which applies. The reply is informational only – it never automatically reopens the task; a premature accept is undone only through the existing revive-from-complete path (the decision immediately below), never by the reply's content. This changes only what `acceptDefaultBody`/`AcceptTldr` say and that a reply is expected – it does NOT change the state machine: the status flip in `AcceptRole` stays authoritative and immediate, exactly as originally speced.

**Decision (refinement #2, post-implementation, found via live-testing): `heraReattach`'s dead-session branch must consult the SAME close-out guard before calling `startSession`, for worker/freelance roles.**

Live-testing `hera_accept` end to end (accept a self-reported-done, session-dead worker, then press `Enter` on its now-`complete` row) surfaced a real gap: `heraReattach`'s dead-session branch called `startSession` unconditionally for every role kind, with zero awareness of Hera close-out state. `startSession` unconditionally flips the task to `in_progress`; the underlying session then exits almost immediately (nothing to resume), and the ordinary post-exit rule rolls the task to `in_review` – silently undoing the accept even though `Enter` is not itself an explicit revive. This is a pre-existing gap in the `heraWorkerAwaitingCloseout`/`Enter` wiring, not new `hera_accept`-specific logic – it would have applied equally to a self-reported-done `ready_to_close` worker with a dead session even before `hera_accept` existed; nobody had tested that exact combination live before. Fix: `heraReattach`'s dead-session branch now calls the renamed, exported `HeraWorkerAwaitingCloseout` predicate (the same one `ReviveHeraWorkerToInProgress` uses) for worker/freelance selections BEFORE calling `startSession`, and refuses (no status write, no session start, a status-bar message) when the task is awaiting close-out. Coordinators are unaffected – they have no `ready_to_close` concept and already had special-cased live-only handling. This makes the "a premature accept can only be undone via an explicit revive" guarantee hold for every UI trigger, not only the two paths (`reviveHeraWorker`'s kick, `hera_revive`) that happened to already call `ReviveHeraWorkerToInProgress`.

**Decision (additive, post-implementation, Aaron-approved to fold into this change): the roster/rail must render a coordinator-accepted worker distinctly from a merely self-reported ready_to_close one.**

Neither `rosterStatusText` (details.go) nor `widget.RoleStatusIcon` ever looked at `RoleView.TaskStatus` — both switch purely on role-state signals (ReadyToClose/NeedsInput/Failed/Done/Idle/Live), so an `hera_accept`-flipped `complete` worker rendered identically to a `ready_to_close` one that a coordinator hasn't acted on yet. Fix: a new `RoleStatusInputs.Accepted` field (`role.TaskStatus == model.StatusComplete.String()`), ranked below `NeedsInput`/`Active` (a role still genuinely blocked or producing output shows that first) and above `ReadyToClose`/`Failed`/`Done`/`Idle`/`Live` (an accept is a coordinator-authoritative terminal signal that supersedes the self-reported ladder). The icon reuses `✓` but bolds it on `theme.StyleComplete` — visibly distinct from plain Done's non-bold `✓` and ReadyToClose's bold clipboard-check icon — and `rosterStatusText` gets a matching `"accepted"` label in the same relative position. Scoped to worker/freelance roster rows only; `coordStatusLabel`/`coordTaskStatusLabel` (the coordinator's own, already-correct terminal-TaskStatus path) are untouched.

**Decision: `hera_accept` mirrors `hera_revive`'s exact authorization shape – coordinator-only, rejects the caller's own role as a target – rather than inventing a new authorization pattern.**

`hera_revive` already established "coordinator-only, live-binding-in-this-orchestrator, target must not be the caller's own role" for a comparable coordinator→role action. Reusing it verbatim (same `resolveCallerRole` + `resolveOrchRole` + self-target check) keeps the two tools' failure modes and error strings consistent for a caller that already knows one.

**Decision: `ReviveHeraWorkerToInProgress` gains `complete` as a second valid source status, with the SAME `heraWorkerAwaitingCloseout` guard re-evaluated (not bypassed) for that source.**

The guard exists to stop a revive from clobbering a genuinely-finished worker still awaiting coordinator close-out (`ready_to_close` meta, or a terminal role-status). Once a task has been explicitly `hera_accept`-ed, though, the coordinator's decision already happened – the worker is no longer "awaiting" anything; it's done and accepted. Re-evaluating the SAME guard (rather than skipping it for the `complete` source) is the conservative choice: if a future caller somehow flips a task to `complete` a different way and it still carries `ready_to_close`/terminal role-status, revive-from-complete still refuses, matching the existing safety story instead of carving out a special case. In practice, an accepted task's role status is whatever it was when accepted (not necessarily terminal), so this guard rarely fires for the accept-then-revive path – but it costs nothing to leave it in force.

**Decision: `Ops.ArchiveToggle` reports which direction just fired (`(archived bool, err error)`), instead of `heraHide` re-deriving it from a second read.**

`ArchiveToggle` already reads the role's CURRENT `archived_at` before deciding archive-vs-unarchive; that read is the one and only place the direction is known. Returning it avoids a redundant second `HeraRole` read in `heraHide` and avoids the two functions ever disagreeing about which branch just ran.

**Decision: the PR-merge nudge needs NO new DB query – it reaches the task's most recent Hera role (any binding, live or ended) via `ListHeraBindingsByTask` (already ordered most-recent-first) + `HeraRole` + `ListHeraRolesByKind(coordinator)`, exactly as the gater's own `holdAndPing`/`pingFanIn` already resolve "the coordinator to notify."**

The eligibility loop's existing terminal-state skip (a `merged`/`merged-closed` cached state permanently excludes a task from ALL future polling) already guarantees a transition into `merged` is observed at most once per task, ever – so the nudge fires exactly once by construction, with no additional "was this already merged" bookkeeping needed. When the resolved role IS itself the coordinator (its own PR merged), the nudge is a self-send and is skipped silently – a coordinator does not need to be told its own PR merged via this channel.

## Risks / Trade-offs

- **[Risk]** The gater's auto-accept only fires for the worker-kind materialize path, not `subcoord` materialization. → **Mitigation**: explicitly scoped and documented (mirrors the existing fan-in notice's own worker-only scope); a sub-coordinator's blockers are still accepted whenever ITS parent's dependents materialize through the ordinary path – only the subcoord node's OWN newly-materialized agent doesn't retroactively accept its blockers via this mechanism today. Named as a follow-up, not silently dropped.
- **[Risk]** `hera_accept`'s notification is best-effort (matches `hera_send`'s own soft-fail delivery contract) – a coordinator that calls it while the target has no live binding still flips status but the reply-required check-in message is queued-no-binding, never delivered, so no reply is ever prompted. → **Mitigation**: this is the existing, well-understood `hera.Service.Send` contract every other hera message already lives with; no new risk introduced. The status flip itself is unaffected either way – delivery failure never blocks or reverts it.
- **[Risk]** `Ops.ArchiveToggle`'s signature change touches several test call sites. → **Mitigation**: mechanical, caught immediately by `go build`/`go vet`; no behavior change for any existing caller that only checked the error.

## Open Questions

None.
