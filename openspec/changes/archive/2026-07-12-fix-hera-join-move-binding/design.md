## Context

A hera-bound task (`11a-archive`, worker under orchestrator `coordctx-exec`) was found holding a *second*, unrelated live binding under a completely different, older orchestrator (`hera-model-tasks`), as a `freelance`-kind role named `11a-archive-report`. This showed up in the TUI's "Freelance" rail section, which is confusing: the same argus task appears to be two different things depending on which orchestrator you look through.

Root cause, confirmed by reading `internal/mcp/hera.go` (`toolHeraJoin`, attach-mode branch, ~line 571-607): attach mode only rejects a *second* binding under the *same* orchestrator the caller is already bound to. It does nothing to check or end bindings the caller already holds under *other* orchestrators — it just creates an additional, disconnected one. Freelance bindings never expire, so the stray binding is permanent until manually removed.

Discovery findings from `openspec/specs/hera-coordination/spec.md`: multi-orchestrator binding is a **deliberate, tested, load-bearing DB-level capability** (Requirement: "Orchestrator, role, and binding storage model", scenario "Task may bind under multiple orchestrators") — it is required for the worker self-promotion / `subcoord` pattern: a task spawned as a worker under orchestrator A can call `hera_new_orchestrator` to become coordinator of a *new* child orchestrator B while *keeping* its worker binding under A. `hera_tree_updates`'s subtree BFS depends on exactly this shape to walk parent→child orchestrator trees. That promotion path is `hera_new_orchestrator`, a separate code path with its own caller-kind guards (rejects an existing coordinator; allows worker/freelance/unbound) — it is not implicated in this bug and must not be touched.

The gap is narrower than "multi-binding is bad": `hera_join`'s attach mode is the only place a caller can attach to an *arbitrary, pre-existing, unrelated* orchestrator with zero structural relationship to its current binding, and it does so silently — no rejection, no warning, no relation to a hierarchy. That's the exact shape of the bug.

**Revision after plan review:** the first draft of this design had `hera_join` silently move the binding by default, with a `keep_existing` opt-out for intentional multi-homing. Feedback: joining a different orchestrator while already bound should be a *deliberate, separate act*, not an implicit side effect of `hera_join` — and there is no legitimate multi-home use case to preserve an escape hatch for. Revised approach: `hera_join` now *rejects* this case outright and redirects the caller to a new, explicit `hera_move` tool. There is no opt-out; `hera_move` unconditionally moves.

## Goals / Non-Goals

**Goals:**

- `hera_join` attach mode rejects the call — rather than silently creating a second binding — when the calling task already holds a live binding under a *different* orchestrator than the one being joined, and directs the caller to the new `hera_move` tool.
- Add `hera_move`, a new MCP tool that explicitly relocates the caller's current live binding to a different orchestrator: ends the old binding (like a normal leave) and creates the new role+binding, transactionally, unconditionally (no opt-out — moving is the tool's entire purpose).
- Update the `hera-coordination` base spec so it accurately describes: `hera_join`'s new rejection case, the new `hera_move` tool, and that unscoped cross-orchestrator multi-binding is no longer reachable through `hera_join` at all — the only sanctioned way to hold 2+ live bindings remains `hera_new_orchestrator` self-promotion.

**Non-Goals:**

- Changing `hera_new_orchestrator`'s existing multi-binding allowance for worker self-promotion / `subcoord` — that path is correct as specced and is untouched.
- Changing the DB-level constraint (partial unique indexes) that permits multiple live bindings across distinct orchestrators — that constraint stays exactly as-is; it's still needed for self-promotion.
- Any opt-in multi-home escape hatch on either `hera_join` or `hera_move` — there is no legitimate use case for it, so none is provided. A caller that genuinely wants two independent bindings has only the self-promotion path.
- Retroactively auditing or cleaning up any *other* stray bindings beyond the one identified — this change fixes the one repro case and closes the code path that created it; a broader audit is out of scope.
- Adding TUI affordances for `hera_move` — it's an MCP tool for agent callers, not a human-facing UI feature.
- Cleaning up the one specific stray binding that prompted this investigation (`hera_bindings.id=596`, role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`/id 66, `argus_task_id` `1783320494974244000`) — dropped 2026-07-12 by Aaron's decision; see Migration Plan. This change closes the code path that creates stray bindings of this shape; it does not touch the one already-existing row.

## Decisions

**Decision: `hera_join` rejects-and-redirects; the actual move lives in a new, separate `hera_move` tool — not a silent default inside `hera_join`.**

Alternatives considered:
1. *`hera_join` moves silently by default* (the pre-feedback design) — rejected: moving is a meaningfully destructive action (it ends a live binding elsewhere) and burying it inside a tool named "join" makes it too easy to trigger by accident, exactly as happened in the bug this fixes. A caller should have to say "move" to get a move.
2. *Add a caller-kind/already-bound guard to `hera_join` that hard-rejects with no redirect* — rejected: leaves the caller with no path forward at all (there's no other tool to relocate a binding), which is worse than today's silent duplicate.
3. **(Chosen) `hera_join` rejects and names `hera_move` as the explicit remedy; `hera_move` is a new, single-purpose tool that performs the end-old+create-new transaction unconditionally.** This matches the mental model "joining is for a fresh/unbound context; moving is its own deliberate act," requires no flags or opt-outs on either tool, and keeps `hera_join` exactly as simple as it is today for every case except the one that was broken.

**Decision: "move" ends the prior binding the same way an operator-initiated leave would — set `ended_at` (and an `end_reason`, e.g. `"moved"`), not delete the row.**

Rationale: bindings are historical records (`hera_tree_updates` and other subtree logic read binding history); deleting rows would break auditability and any downstream logic that inspects the ended binding's `end_reason` (mirroring how J-adopt/detach already end bindings rather than delete them, per `internal/tui/heraactions.go`).

**Decision: `hera_move` requires the caller to already hold exactly one resolvable live binding (using the existing `from_orchestrator`-disambiguation pattern when it holds 2+), and rejects both "nothing to move" and "move to the same orchestrator" as errors with redirects.**

Rationale: mirrors the existing "Caller role resolution from cwd with orchestrator disambiguation" pattern already used by other hera tools (`internal/mcp/hera.go:218` `resolveCallerRole`), so the UX is consistent instead of inventing a new disambiguation shape. An unbound caller has nothing to move (directed to `hera_join`/`hera_new_orchestrator`); a same-orchestrator target is a no-op (directed to `hera_join` claim mode, which already reports the current binding).

**Decision: `hera_move` rejects `kind=coordinator`, mirroring `hera_join`.**

Rationale: coordinators are bootstrapped via `hera_new_orchestrator`, never joined or moved into; keeping this rule identical across `hera_join` and `hera_move` avoids a second, different coordinator-creation path.

## Risks / Trade-offs

- **Risk:** An existing caller relying on today's silent-duplicate behavior for a legitimate reason breaks once this ships, with no opt-out available. → **Mitigation:** grep of the codebase/skills found no documented workflow that calls `hera_join` attach mode while already bound elsewhere and expects both bindings to survive; the only such caller found in the wild (`11a-archive`'s stray binding) was the accidental case this change fixes. Given no legitimate use case was found, no escape hatch is provided by design (see Non-Goals).
- **Risk:** Ending the prior binding as part of `hera_move` could race with other in-flight operations against that binding (e.g., a concurrent `hera_send` targeting the soon-to-be-ended role). → **Mitigation:** perform the end-binding and create-new-binding as one transaction, matching the existing transactional pattern in `CreateHeraRoleWithBinding`/`hera_new_orchestrator`.
- **Trade-off:** A caller that hits the new `hera_join` rejection must make a second tool call (`hera_move`) instead of the join "just working." → Accepted: this is the explicit point of the revision — moving is deliberate, not implicit.

## Migration Plan

- Code change ships as a normal PR; no schema migration needed (no new columns — `ended_at`/`end_reason` already exist on `hera_bindings`).
- The one-off live-DB fix for the stray binding (`hera_bindings.id=596`) was dropped 2026-07-12: the write is sandbox-blocked from any agent session, and not worth Aaron hand-running raw SQL against `~/.argus/data.sql` for one cosmetic row. No data fix is part of this change; there is nothing to roll back.
- Rollback: revert the PR.

## Open Questions

None — scope and behavior were confirmed directly with the user, including a plan-review revision (reject-and-redirect via a new `hera_move` tool, no opt-out), before finalizing this design.

## Alternatives considered

See "Decisions" above — each decision lists the alternatives evaluated inline.

## Acceptance criteria

- It should reject `hera_join` attach mode with an error directing the caller to `hera_move`, and end/create no binding, when the calling task already holds a live binding under a *different* orchestrator than the one being joined.
- It should leave `hera_join` attach mode's other behaviors unchanged: an unbound caller still creates a new binding normally; a caller already bound under the *same* target orchestrator is still rejected and directed to `hera_join` claim mode.
- It should, on `hera_move`, end the caller's resolved current live binding (`ended_at`/`end_reason: "moved"`) and create a new role+binding under the target orchestrator, transactionally, when the caller holds exactly one live binding (or has disambiguated via `from_orchestrator`).
- It should reject `hera_move` with a "nothing to move" error directing to `hera_join`/`hera_new_orchestrator` when the caller holds no live binding.
- It should reject `hera_move` with a no-op error directing to `hera_join` claim mode when the resolved source orchestrator equals the target orchestrator.
- It should require `from_orchestrator` on `hera_move` (mirroring existing disambiguation errors) when the caller holds 2+ live bindings, and succeed once it's supplied.
- It should reject `hera_move` with `kind=coordinator`, directing the caller to `hera_new_orchestrator`, mirroring `hera_join`.
- It should leave `hera_new_orchestrator`'s behavior completely unchanged (worker self-promotion to a new child orchestrator while retaining the worker binding under the parent still succeeds with no binding ended).
- It should report, in the `hera_move` response, the source orchestrator + role name that was moved, plus the new binding id.
