## Context

A hera-bound task (`11a-archive`, worker under orchestrator `coordctx-exec`) was found holding a *second*, unrelated live binding under a completely different, older orchestrator (`hera-model-tasks`), as a `freelance`-kind role named `11a-archive-report`. This showed up in the TUI's "Freelance" rail section, which is confusing: the same argus task appears to be two different things depending on which orchestrator you look through.

Root cause, confirmed by reading `internal/mcp/hera.go` (`toolHeraJoin`, attach-mode branch, ~line 571-607): attach mode only rejects a *second* binding under the *same* orchestrator the caller is already bound to. It does nothing to check or end bindings the caller already holds under *other* orchestrators — it just creates an additional, disconnected one. Freelance bindings never expire, so the stray binding is permanent until manually removed.

Discovery findings from `openspec/specs/hera-coordination/spec.md`: multi-orchestrator binding is a **deliberate, tested, load-bearing DB-level capability** (Requirement: "Orchestrator, role, and binding storage model", scenario "Task may bind under multiple orchestrators") — it is required for the worker self-promotion / `subcoord` pattern: a task spawned as a worker under orchestrator A can call `hera_new_orchestrator` to become coordinator of a *new* child orchestrator B while *keeping* its worker binding under A. `hera_tree_updates`'s subtree BFS depends on exactly this shape to walk parent→child orchestrator trees. That promotion path is `hera_new_orchestrator`, a separate code path with its own caller-kind guards (rejects an existing coordinator; allows worker/freelance/unbound) — it is not implicated in this bug and must not be touched.

The gap is narrower than "multi-binding is bad": `hera_join`'s attach mode is the only place a caller can attach to an *arbitrary, pre-existing, unrelated* orchestrator with zero structural relationship to its current binding, and it does so silently — no rejection, no warning, no relation to a hierarchy. That's the exact shape of the bug.

## Goals / Non-Goals

**Goals:**

- `hera_join` attach mode defaults to *moving* the caller's live binding: if the calling task already holds a live binding under a different orchestrator, end it (like a normal leave — set `ended_at`) before creating the new role+binding.
- Preserve an explicit escape hatch (`keep_existing: true`) for the rare deliberate multi-home case, so nothing is permanently foreclosed.
- Clean up the one specific stray binding that prompted this investigation (`hera_bindings.id=596`, role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`/id 66, `argus_task_id` `1783320494974244000`).
- Update the `hera-coordination` base spec so it accurately describes the new default and clarifies that unscoped multi-binding now only occurs via `hera_new_orchestrator` self-promotion or an explicit `keep_existing` override — not silently via plain `hera_join`.

**Non-Goals:**

- Changing `hera_new_orchestrator`'s existing multi-binding allowance for worker self-promotion / `subcoord` — that path is correct as specced and is untouched.
- Changing the DB-level constraint (partial unique indexes) that permits multiple live bindings across distinct orchestrators — that constraint stays exactly as-is; it's still needed for self-promotion.
- Retroactively auditing or cleaning up any *other* stray bindings beyond the one identified — this change fixes the one repro case and closes the code path that created it; a broader audit is out of scope.
- Adding TUI affordances for the `keep_existing` flag — it's an MCP tool parameter for agent callers, not a human-facing UI feature.

## Decisions

**Decision: move-by-default lives in `hera_join` attach mode only, not as a global DB/service-layer rule.**

Alternatives considered:
1. *Enforce single-binding at the DB layer* (drop the multi-orchestrator unique-index allowance entirely) — rejected: breaks the specced, tested self-promotion/subcoord pattern outright.
2. *Add a caller-kind guard to `hera_join` like `hera_new_orchestrator` has* (reject if caller already holds any other live binding) — rejected: too blunt. A hard rejection forces the caller to manually end its old binding first via some other call, and no such "leave" tool exists today — this would just turn a silent duplicate into a dead end.
3. **(Chosen) Move-by-default with opt-in override** — attach mode ends any live binding under a *different* orchestrator before creating the new one, unless the caller passes `keep_existing: true`. This matches the mental model "joining a new coord replaces membership in the old one," requires no new "leave" tool, and the override keeps the door open for a genuinely intentional multi-home case without the tool doing it silently by default.

**Decision: "move" ends the prior binding the same way an operator-initiated leave would — set `ended_at` (and an `end_reason`, e.g. `"moved"`), not delete the row.**

Rationale: bindings are historical records (`hera_tree_updates` and other subtree logic read binding history); deleting rows would break auditability and any downstream logic that inspects the ended binding's `end_reason` (mirroring how J-adopt/detach already end bindings rather than delete them, per `internal/tui/heraactions.go`).

**Decision: `keep_existing` is a boolean on `hera_join`, not a separate tool.**

Rationale: attach mode is a single MCP call; adding a parameter is the smallest surface change and mirrors the existing `kind` enum parameter pattern already on this tool. A separate "hera_join_multi" tool would be a needless surface increase for a rare case.

## Risks / Trade-offs

- **Risk:** An existing caller relying on today's silent-duplicate behavior for a legitimate reason breaks once this ships. → **Mitigation:** grep the codebase/skills for any documented workflow that calls `hera_join` attach mode while already bound elsewhere and expects both bindings to survive; none found during discovery (the only such caller found in the wild — `11a-archive`'s stray binding — was the accidental case this change fixes). If one turns up later, `keep_existing: true` covers it without further code changes.
- **Risk:** Ending the prior binding as part of `hera_join` could race with other in-flight operations against that binding (e.g., a concurrent `hera_send` targeting the soon-to-be-ended role). → **Mitigation:** perform the end-binding and create-new-binding as one transaction, matching the existing transactional pattern in `CreateHeraRoleWithBinding`/`hera_new_orchestrator`.
- **Trade-off:** A caller that *meant* to multi-home but forgot `keep_existing: true` silently loses its old binding instead of getting an error. → Accepted: this matches the user's stated mental model (join = move by default) and is the whole point of the fix; the tool's response text will explicitly state which prior binding (if any) was ended, so the caller sees it happened.

## Migration Plan

- Code change ships as a normal PR; no schema migration needed (no new columns — `ended_at`/`end_reason` already exist on `hera_bindings`).
- The one stray binding identified in this investigation (`hera_bindings.id=596`) is fixed with a one-off, targeted `UPDATE` (setting `ended_at`/`end_reason`) run once against the live `~/.argus/data.sql`, per this repo's breaking-changes policy ("no legacy migration code — write a one-off script for schema data moves"). This is a live-data fix, not part of the shipped code path, and is not a reusable migration.
- Rollback: revert the PR; the one-off data fix is not reverted (ending a stray binding is not something to undo).

## Open Questions

None — scope, behavior, and the one-off cleanup were confirmed directly with the user before scaffolding this change.

## Alternatives considered

See "Decisions" above — each decision lists the alternatives evaluated inline.

## Acceptance criteria

- It should end a caller's live binding under orchestrator A when it calls `hera_join` attach mode targeting a different orchestrator B, before creating the new binding.
- It should NOT end any existing binding when `hera_join` attach mode targets an orchestrator the caller has no live binding under and the caller holds no other live bindings at all (i.e. the existing unbound-task case is unchanged).
- It should skip ending any prior binding when the caller passes `keep_existing: true`, preserving today's duplicate-binding behavior for that call.
- It should still reject attach mode outright when the caller already holds a live binding under the *same* target orchestrator (unchanged existing behavior — this is a same-orchestrator conflict, not a cross-orchestrator move).
- It should leave `hera_new_orchestrator`'s behavior completely unchanged (worker self-promotion to a new child orchestrator while retaining the worker binding under the parent still succeeds with no new binding ended).
- It should report, in the `hera_join` attach-mode response, which prior binding (orchestrator + role name) was ended, when one was.
