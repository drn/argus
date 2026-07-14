## Context

A born-bound worker (materialized from a plan-DAG node) got permanently stuck: `hera_join(cwd, orchestrator=X)` claim mode said "no binding exists" while attach mode (`role_name`+`kind` supplied) failed with a raw `UNIQUE constraint failed: hera_bindings.worktree_path, hera_bindings.orchestrator_id` — a binding already exists. Both statements describe the SAME `(worktree_path, orchestrator_id)` pair.

Confirmed against the live `~/.argus/data.sql` (read-only): two argus tasks share one worktree path — a stale, archived `in_review` task (orchestrator `restore-fork-variants`, its hera binding already ENDED) and the live `in_progress` worker (orchestrator `sketch-blueprint-comments-apply`, its hera binding LIVE). `resolveTask` (`internal/mcp/server.go`, the shared cwd→task resolver every cwd-addressed MCP tool uses) resolves a `cwd` by finding the task whose `Worktree` is the longest matching prefix; before this change, when two tasks tie for that longest match — which only happens when their `Worktree` values are the literal same string — it silently kept whichever task the DB happened to list first. A `cwd` could therefore resolve to the STALE task id, so the task-keyed claim (`HeraLiveBindingByTaskAndOrchestrator`) missed the live binding that the worktree-keyed uniqueness index (`idx_hera_bindings_live_worktree_orch`) nonetheless rejected on an attach INSERT. The same first-match hazard is what let `argus_clipboard_set(cwd=…)` resolve to a stale 17-day-old task elsewhere — `resolveTask` is shared plumbing, so the fix belongs at the resolver, not in one hera handler.

This is the third recurrence of the plan-node/binding race family (early materialization races, fixed via transactional `hera_plan`/`CreateHeraRoleWithBinding`; the one-directional cross-orchestrator binding-lookup gap, fixed by `hera_move` in #847; now the shared-worktree collision), so this fix matches that precedent's rigor — a deterministic invariant plus regression tests, not a point patch.

A standalone reference implementation already landed on the separate `anutron/hera` repo as PR #156. That repo models bindings with a flat `bindings` table and a `Resolver`/`JoinHandler` split; this change ports the same fix shape onto argus's native structure — `internal/db/hera.go`'s `HeraBinding`/`HeraRole`/`HeraOrchestrator` DAOs and `internal/mcp/hera.go`'s `resolveCallerRole`/`toolHeraJoin`/`toolHeraNewOrchestrator` handlers — because that is the code the deployed daemon actually runs. Two structural differences from the reference worth calling out:

- Native already had an UNSCOPED worktree-keyed lookup, `HeraLiveBindingByWorktree` (added at the M1 schema commit, previously unused), which returns `ErrHeraAmbiguous` when 2+ *different orchestrators* hold a live binding at the same worktree — the legitimate multi-binding shape. This change adds only the missing orchestrator-SCOPED twin (`HeraLiveBindingByWorktreeAndOrchestrator`) rather than re-deriving both from scratch, and reuses the existing ambiguous-aware unscoped lookup for the claim path's "no orchestrator supplied" branch.
- Native's `resolveTask` does longest-prefix matching (so a `cwd` nested inside a worktree still resolves), not the reference's exact-match-only `TaskForCwd`. The disambiguation is applied to whichever tasks tie for the longest match, which — as shown above — can only be tasks whose `Worktree` values are identical strings, so the same "one match unchanged; drop archived; prefer the single in_progress" ladder applies unmodified.

## Goals / Non-Goals

**Goals:**

- `resolveTask` never silently resolves a `cwd` to a stale task when a live task shares the same worktree path.
- Every binding-identity lookup that keys off `argus_task_id` (claim, `resolveCallerRole`'s single-binding and orchestrator-scoped paths, the attach/bootstrap collision guards) falls back to a worktree-keyed lookup on a task-keyed miss, so claim and attach can never disagree about "which task is this."
- Attach (`hera_join`) and bootstrap (`hera_new_orchestrator`) never leak a raw `UNIQUE constraint failed` error — a worktree collision gets an actionable message.
- A supported, non-destructive repair path (`hera_rebind`) exists for the harder case: the live binding's OWN `argus_task_id` has drifted, so even the task-then-worktree fallback still resolves nothing under the caller's actual task id (only the worktree-keyed side sees it) — the row itself needs to change, not just the lookup.
- A born-bound plan-DAG node's worker (the reported repro shape) composes correctly: after materialization creates its binding, a plain `hera_join` claim from the node's worktree succeeds even if a stale sibling task shares that worktree.

**Non-Goals:**

- Changing the DB schema or the live-uniqueness indexes — both are correct as-is; the bug is in the *lookup* layer, not the constraint layer.
- Auditing or repairing the specific already-stuck live binding found in `~/.argus/data.sql` — the daemon's own fixed code path (a restart, or `hera_rebind` once deployed) is how any live repair happens; no manual SQL edit is part of this change.
- Changing `agent.CreateAndStart`'s worktree-naming/reuse behavior — that a worktree directory CAN be reused across a task's lifecycle is an existing, accepted argus mechanic (worktree cleanup is a separate concern); this change makes hera's identity resolution correct in the face of it, not the reuse itself.
- A general audit of every other cwd-addressed tool for the same first-match hazard beyond `resolveTask` itself (which is shared plumbing, so fixing it there fixes all of them structurally) — no other tool has its own separate cwd→task resolution path.

## Decisions

**Decision: fix `resolveTask` itself (shared plumbing), not just the hera call sites.**

`resolveTask` (`internal/mcp/server.go`) backs every cwd-addressed MCP tool — clipboard, task management, messaging, and hera. The bug's root cause (first-match-wins on a worktree tie) lives entirely in that one function. Fixing it there closes the door for every current and future caller, including the sibling `argus_clipboard_set` bug the incident report names as a symptom of the same hazard, rather than adding a hera-local workaround.

**Decision: two-layer fix — resolver disambiguation AND a task-then-worktree lookup fallback — because they cover different failure shapes.**

Disambiguating `resolveTask` fixes the common case (the binding row itself is correct; only the cwd→task lookup picked the wrong task). But a binding's own `argus_task_id` can independently drift from reality (e.g. if a task was recreated with a new id at the same worktree without the binding being updated) — in that case even a correctly-resolved `cwd` still won't task-key-match the binding. The lookup fallback (`HeraLiveBindingByWorktreeAndOrchestrator`, wired through `resolveCallerRole`'s new `liveBindingForOrch`/`liveBindingForTask` helpers) covers that second shape: it resolves the exact binding an attach INSERT would collide with, regardless of which task id cwd resolution landed on. Orchestrator scoping is what keeps this safe — a stale binding under a *different* orchestrator sharing the worktree is never returned, preserving the legitimate multi-binding shape (`internal/db/hera.go`'s existing `TestHeraMultiBinding` tests).

**Decision: claim never rewrites a binding's `argus_task_id`; repair is a separate, explicit `hera_rebind` call.**

In the reported incident the live binding row was already correct — only the *lookup* was wrong, which the fallback above fixes without touching any row. Silently "fixing" a binding's `argus_task_id` as a side effect of a read-only claim would make claim a hidden write with surprising blast radius (it could steal a slot from a role that legitimately holds it). `hera_rebind` is the explicit, opt-in act for the harder drift case, and it still refuses rather than guesses when the state is genuinely ambiguous.

**Decision: `hera_rebind` ends the stale binding and creates a fresh one under the SAME role, rather than deleting/recreating the role.**

Bindings are task↔role incarnation links; the role (and its prompt, messages, and status — all keyed on `role_id`) is the durable identity a coordinator and worker have been messaging against. Ending the old binding (mirroring `hera_move`'s pattern: `end_reason` stamped, not deleted, preserving binding history for the subtree TLDR roll-up) and inserting a clean one under the same role reuses the DAO's uniqueness enforcement and preserves everything the worker cares about, while making both lookup paths agree. This is explicitly the human operator's requested behavior: a supported repair path, not "spawn a replacement worker and cancel the plan node" (which would discard the stuck worker's live session and context).

**Decision: `hera_rebind` candidate gathering unions the task-keyed and worktree-keyed single-row lookups (each capped at one row per orchestrator by the live-uniqueness indexes) rather than adding a new "list by worktree" DAO method.**

The reference implementation (anutron/hera PR #156) added a `ListLiveByWorktree` DAO twin because its `CallerRole`'s no-orchestrator path needed a "list all, then decide ambiguous" shape. Native's equivalent, `HeraLiveBindingByTask`, already implements exactly that shape via a single query with an ambiguous-row count check (`heraSingleLiveBinding`) — and its existing unscoped sibling `HeraLiveBindingByWorktree` (see Context) already does the same for worktree-keyed lookups. So `resolveCallerRole`'s no-orchestrator fallback needs no new list method: a task-keyed miss falls back directly to the existing `HeraLiveBindingByWorktree`. `hera_rebind`'s candidate-gathering (which needs bindings scoped to ONE named orchestrator) unions the two *orchestrator-scoped* single-row lookups instead, since each is capped at one row by its respective unique index — a list method would return at most the same two rows a manual union already produces.

## Risks / Trade-offs

- **Risk:** The worktree-keyed fallback could, in principle, resolve a binding the caller didn't "mean" if two *unrelated* tasks happen to share a worktree path under the same orchestrator at the same time. → **Mitigation:** the live-uniqueness index (`worktree_path`, `orchestrator_id`) caps this at exactly one row per orchestrator — there is no "wrong" binding to pick, only the one and only live row at that physical location, which is the same row an attach INSERT would have collided with regardless.
- **Risk:** `resolveTask`'s new disambiguation could reject a call that previously silently "worked" (by luck) when 2+ live `in_progress` tasks genuinely share a worktree. → **Accepted:** that state is itself a bug elsewhere (two argus tasks should not both be `in_progress` at the identical worktree path); refusing with a named-candidates error surfaces it for cleanup instead of nondeterministically picking one, which is strictly safer than the prior silent behavior.
- **Trade-off:** `hera_rebind` adds a fourteenth `hera_*` tool and a small amount of candidate-disambiguation logic (role_name-based) that mirrors `hera_move`'s `from_orchestrator` disambiguation pattern. Accepted: a supported repair path was an explicit ask, and reusing the established disambiguation shape (rather than inventing a new one) keeps the UX consistent with `hera_move`.

## Migration Plan

- Code change ships as a normal PR; no schema migration (the `worktree_path`/`orchestrator_id` columns and their unique index already exist from the M1 schema).
- No live-DB repair is part of this change. Any already-stuck binding is reconciled by the daemon's own fixed code path once this ships and the daemon is rebuilt/restarted (the fallback fixes claim immediately; `hera_rebind` is available for the drifted-task-id case).
- Rollback: revert the PR. No data written by this change needs undoing (it only adds a lookup method, a resolver disambiguation, two collision pre-checks, and a new opt-in repair tool).

## Open Questions

None — root cause was confirmed directly against the live DB before this design was written, and the fix shape mirrors the already-reviewed and merged `anutron/hera` PR #156.

## Acceptance criteria

- It should resolve a `cwd` shared by a stale/archived task and a live `in_progress` task to the live task, not whichever task the DB lists first.
- It should refuse (naming the candidates), rather than guess, when a `cwd` maps to two or more live `in_progress` tasks sharing a worktree.
- It should let a plain `hera_join` claim succeed whenever a `hera_join` attach for the same `(cwd, orchestrator)` would be rejected by the live-uniqueness index — the two must never disagree.
- It should let `hera_join` claim succeed via the worktree-keyed fallback even when the live binding's own `argus_task_id` differs from the cwd-resolved task's id (as long as cwd resolution itself is unambiguous).
- It should make `hera_join` attach and `hera_new_orchestrator` bootstrap return an actionable message — never a raw `UNIQUE constraint failed` — when a live binding already occupies the caller's `(worktree_path, orchestrator)` slot, whether keyed by the caller's exact task id or a drifted one.
- It should provide `hera_rebind(cwd, orchestrator, [role_name])` that reconciles a drifted binding to the caller's real live task without deleting the role (its prompt, messages, and status survive), is a no-op when already consistent, and refuses (changing nothing) when genuinely ambiguous: two live `in_progress` tasks share the worktree; multiple roles hold a live binding here with no `role_name` to disambiguate; a different role already holds the target task or worktree slot; or there is nothing to reconcile.
- It should leave a born-bound plan-DAG worker's post-materialization `hera_join` claim working exactly as before when no worktree collision exists (the overwhelmingly common case) — no regression to the single-match path.
