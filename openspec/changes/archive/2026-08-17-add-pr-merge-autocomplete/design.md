## Context

`internal/daemon.pollPRStatesOnce` already writes `PRResult.Merged` per task and, on a genuine merge, calls `notifyCoordinatorOfMergedPR` — a nudge-only message to the task's resolved Hera coordinator, deliberately never an automatic status flip (`add-hera-accept-lifecycle`, documented in `context/knowledge/gotchas/orchestration.md` and `openspec/specs/pr-status/spec.md`). This change adds a second, independent notification target: the task's OWN Hera role, asked to self-assess using tools it already has (`hera_send`/`hera_status` to inform its coordinator, `task_complete` to close itself out).

An earlier draft of this change instead reused the shared `internal/hera.AcceptRole` primitive (the coordinator-only `hera_accept` tool's completion path, also used by the plan-DAG gater's auto-accept) to auto-flip a worker's task to `complete` directly, narrowly gated on worker-kind + exact `in_review` status. That approach was set aside after review — see "Decision 1" below for why.

## Goals / Non-Goals

**Goals:**
- The moment a hera-bound task's PR is confirmed merged, put the news directly in front of the role that did the work, framed as an actionable self-assessment prompt, not just a coordinator-facing FYI.
- Introduce zero new completion machinery — the agent's own existing tools (`task_complete`, `hera_send`, `hera_status`) are sufficient; the daemon's job is delivery, not judgment.
- Never regress the existing coordinator nudge.

**Non-Goals:**
- Not touching `AcceptRole`, `hera_accept`, or the gater's `acceptBlockers` — they are unmodified.
- Not attempting any daemon-side heuristic for "is this task really done" (no exactly-one-PR check, no plan-DAG dependents check, no unresolved-inbox check). Those were considered and explicitly dropped in favor of trusting the agent's own judgment.
- Not building a fallback delivery channel for the self-send case (a coordinator's own directly-bound task) — that gap is accepted as-is.

## Decisions

**Decision 1: Ask the agent, don't have the daemon decide.**
The first draft of this change auto-completed a worker's task via `AcceptRole`, gated on worker-kind + exact `in_review`, reasoning that this narrowed the trigger enough to resolve the ambiguity the original "never auto-accept" decision was concerned about (squash-merged/folded-in workers with no PR of their own can never produce `Merged==true` for their own task). On review, this was set aside for a simpler and more robust design: instead of the daemon inferring "done" from structural DB signals, tell the role itself and let it decide. This sidesteps the ambiguity question entirely rather than needing to carefully argue it away — the agent has full context (what it was asked to do, whether there's more to it) that no combination of DB heuristics can fully substitute for.

**Decision 2: No role-kind or task-status gating on the new notification.**
Since the daemon is no longer making a completion judgment call, there is no need to restrict this to worker-kind roles or to a task already resting `in_review`. A coordinator's or freelance role's own merged PR is an equally valid prompt for the same self-assessment question, and a role still actively `in_progress` simply reads the notice, recognizes it still has more to do, and continues uninterrupted. The gate is entirely the agent's own read of the message, not a precondition on it being delivered.

**Decision 3: Additive, not exclusive, alongside the existing coordinator nudge.**
The existing `notifyCoordinatorOfMergedPR` nudge is left completely unchanged and fires independently. This is a deliberate difference from the first draft, which skipped the coordinator nudge whenever its worker-directed auto-complete fired (to avoid a stale "worth reviewing" message right after an auto-accept). Since this design never changes any status, there is nothing to render "stale" — the coordinator's nudge is still exactly as informative as before, and the two messages serve different purposes (actionable instruction to the role that can act vs. passive visibility for the coordinator).

**Decision 4: The self-send limitation is accepted, not engineered around.**
`db.SendHeraMessage` hard-rejects `fromRoleID == toRoleID` (`ErrHeraMessageSelfSend`) with no exception. When the resolved role IS the coordinator (a coordinator's own task, directly bound, with its own PR), the role-notify has no legal sender/recipient pair to use — `resolveHeraRoleAndCoordinator` always returns the SAME role as both "role" and "coord" in that case. Building a separate self-addressed delivery channel (e.g. a raw PTY-input note bypassing hera_messages entirely, mirroring the host-suspend watchdog's system notes) was considered and rejected as disproportionate scope for a narrow edge case; the existing coordinator-nudge self-skip already treats this scenario as "no news needed via this channel," and this change simply inherits that same practical boundary for its own notification.

## Risks / Trade-offs

- **[Risk]** A role could receive this notice while mid-task and simply ignore it (no enforcement that it actually reads or acts on the message). → **Mitigation**: this is no worse than the pre-existing coordinator nudge, which has the same property; delivery uses the same reliable-notify mechanism as every other hera message, so it reaches a live, idle session promptly.
- **[Risk]** A coordinator's own directly-bound task never gets this notice (self-send is structurally blocked). → **Mitigation**: accepted as a narrow, existing-pattern-consistent gap (Decision 4); the coordinator already doesn't get the OTHER nudge for its own merged PR either, for the same underlying "it does not need telling" reasoning.
- **[Trade-off]** This is still a one-shot decision per task (once the poller caches a terminal `merged-closed` state, that task is never re-evaluated), so a transient DB read error at the one eligible cycle permanently loses the chance to notify. → **Mitigation**: accepted, matching `notifyCoordinatorOfMergedPR`'s own pre-existing one-shot, silent-on-resolution-miss contract; the underlying reads are local SQLite calls, not network calls.

## Migration Plan

No data migration. Purely additive behavior inside an existing background loop; ships and rolls back with a normal daemon deploy/restart. No new config or feature flag — the existing `pr-poller.disabled` kill-switch already covers pausing this code path along with the rest of the poller if needed.

## Open Questions

None.
