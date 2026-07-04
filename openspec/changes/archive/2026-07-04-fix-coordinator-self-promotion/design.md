# Design: fix coordinator self-promotion

## Context

`hera_new_orchestrator` (`internal/mcp/hera.go:421` `toolHeraNewOrchestrator`) resolves the calling task via `cwd` and binds it as the coordinator of a named orchestrator. Its intended uses:

1. **Fresh bootstrap** – a brand-new task declares itself the coordinator ("I am the coordinator"). No prior binding.
2. **Worker self-promotion** – a task already running as a *worker* additionally becomes coordinator of a new child orchestrator (the documented sub-team pattern). The task holds a live worker binding at call time.

The bug is a third, unintended use: a task that is **already a coordinator** calls it again with a new orchestrator name. The existing guard (`internal/mcp/hera.go:456`, `HeraLiveBindingByTaskAndOrchestrator`) only rejects a re-bind under the *same* orchestrator, so a *new* name sails through and the task acquires a second coordinator binding — a phantom nested "sub-coordinator" on the same PTY. In the live repro that pseudo-sub-coordinator then implemented the work solo instead of dispatching.

The #835 prose fix reframes the orientation to dispatch-don't-implement, but prose is advisory. This change adds the enforcing guardrail.

## Goals

- Make it impossible for a coordinator to bind its own session as a second coordinator via `hera_new_orchestrator`.
- Preserve the two legitimate uses (fresh bootstrap, worker self-promotion) unchanged.
- Return an actionable error that points at the correct pattern (spawn a worker; `kind=subcoord` for real sub-teams).

## Non-Goals

- Changing programmatic coordinator creation: the rail `n` key (`SpawnHeraCoordinator`) and gater sub-coord materialization (`MaterializeHeraSubCoordinator`) do not route through the MCP tool and are out of scope — the footgun is agent-driven only.
- Removing the prose fix. Prose + code are complementary (steer + enforce).
- Any change to `hera_join`, `hera_spawn_worker`, or the plan tools.

## Decisions

### D1 — Guard on "caller already holds a live coordinator-kind binding under a DIFFERENT orchestrator"

Iterate the caller task's live bindings (`ListHeraLiveBindingsByTask(task.ID)`); for each, look up the role (`HeraRole(binding.RoleID)`) and reject if any is a coordinator binding under an orchestrator whose name differs from the requested one. This precisely separates the cases:

- fresh bootstrap → zero bindings → allowed;
- worker self-promotion → only worker/freelance bindings → allowed;
- coordinator self-invoke for a NEW orchestrator → a coordinator binding under a different orchestrator → **rejected** (the footgun);
- coordinator re-calling for the SAME orchestrator → skipped here, handled by the existing same-orchestrator guard (D2) with its `hera_join` guidance.

The different-orchestrator scoping matters: the footgun is a coordinator minting a *second, distinct* orchestrator on its own session. A same-orchestrator re-call is a different situation (re-attach / mistake) that the existing guard already serves with the right hint (`hera_join`) — preempting it with the anti-self-promotion message would be less apt. Rejecting a *coordinator* (root or sub) from self-promoting to a new orchestrator is consistent with the corrected model: a coordinator dispatches; deeper sub-teams come from spawning a new session (worker-promotion / `kind=subcoord`), never from relabeling the coordinator's own session.

### D2 — Run the guard EARLY, before `CreateHeraOrchestrator`

`CreateHeraOrchestrator` is idempotent-fetch but *creates* a brand-new orchestrator for an unseen name. If the guard ran after it (like the existing same-orchestrator check at line 456), a rejected coordinator call would leave an orphan empty orchestrator behind. So the new guard runs right after `resolveTask`, before any orchestrator is created. The existing same-orchestrator guard stays where it is (it only fires for an already-existing, already-bound orchestrator, so no orphan is created there).

### D3 — Actionable error message

The error names the caller's current coordinator orchestrator and directs: dispatch actual work with `hera_spawn_worker` (its `project=` targets any repo, so cross-repo needs no new orchestrator); for a genuine multi-project/multi-phase sub-team, spawn a worker that promotes itself or author a `kind=subcoord` plan node.

### D4 — Authoritative signal is the binding, not `task_meta`

`meta:hera.role=coordinator` is a best-effort display mirror and can be stale/absent on historical rows. The live binding + role kind is the source of truth, so the guard reads bindings, not meta.

## Alternatives considered

- **Prose-only (status quo before this change).** Rejected: an LLM can and did ignore orientation text. Prose stays as guidance, not the sole defense.
- **Reject ALL second bindings on a task.** Rejected: breaks worker self-promotion (worker + coordinator on one task is the documented pattern) and legitimate multi-binding cases.
- **Check `meta:hera.role`.** Rejected: meta is best-effort/non-authoritative (D4).
- **Guard inside the store (`CreateHeraRoleWithBinding`).** Rejected: the store is a general primitive also used by programmatic paths (rail `n`, gater) that legitimately create coordinator bindings; the guard belongs at the agent-facing MCP tool where the footgun lives (Non-Goals).

## Risks / Trade-offs

- **Over-blocking a legitimate flow.** Low: the three cases are cleanly separated by binding kind (D1); worker-promotion and fresh bootstrap are covered by explicit tests. A coordinator legitimately needing a sub-team already has non-self paths (worker-promotion, `kind=subcoord`).
- **Performance.** `ListHeraLiveBindingsByTask` + a per-binding role lookup, on a single tool call — negligible (a task has a handful of bindings at most).

## Acceptance criteria

Guard behavior (maps to scenarios in the delta):

- it should reject `hera_new_orchestrator` when the caller task already holds a live coordinator binding under any orchestrator, creating no new orchestrator or binding.
- it should include actionable guidance in that error (spawn a worker; `kind=subcoord` for a real sub-team).
- it should still allow a caller holding only a worker binding to create a new orchestrator + coordinator role (worker self-promotion).
- it should still allow a caller with no binding to bootstrap a fresh orchestrator + coordinator role.
- it should not create an orphan orchestrator when it rejects a coordinator caller (guard runs before orchestrator creation).

## Migration Plan

None. No schema change, no data migration. Behavior-only change to one MCP tool. Archived into `hera-coordination` within PR #835.

## Open Questions

None.
