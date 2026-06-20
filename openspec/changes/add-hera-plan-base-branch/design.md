## Context

The `add-hera-plan-substrate` change gave hera a durable plan-DAG: planned nodes (worker roles with no binding), `hera_blocks` edges, and a gater that materializes a planned node into a live worker once all its blockers reach role-status `done`. At materialization the gater resolves the new worktree's `base_branch` so that a stacked chain builds on its upstream: `resolveBaseBranch` (`internal/heragater/heragater.go:306`) returns the branch of the most-recently-bound `done` blocker, giving clean stacked PRs.

That resolution only covers nodes that **have** blockers. A **root node** (no blockers) has nothing to derive from, so `resolveBaseBranch` returns `""`, and `CreateAndStart` falls back to the project default branch (`projCfg.Branch`, e.g. `master`) via `internal/agent/create.go:148-150`. Consequently a whole plan-DAG always starts off the project default, and there is no way to seed a plan onto a different base — you cannot author a plan-DAG that stacks on unmerged work-in-progress (a feature branch).

This change lets a plan-DAG start off an arbitrary base branch.

## Goals / Non-Goals

**Goals:**

- A root node materializes off the orchestrator's coordinator branch by default, so a DAG "starts wherever the coordinator sits."
- An optional explicit base branch, set when bootstrapping the orchestrator, overrides that default — the literal "start off anything" knob.
- Stay backward-compatible: a coordinator on `master` still yields roots off `master`, exactly as today.
- Leave non-root (blocker-having) base resolution and fan-in behavior unchanged.

**Non-Goals:**

- Per-node or per-plan base branches. The base is a property of the whole coordination effort, set once on the orchestrator.
- Re-basing an already-materialized worker. The base is resolved once, at materialization, as today.
- Changing how stacked chains derive from blockers (the most-recent-bound-blocker rule stands).
- Any TUI/view change. This is substrate only.

## Decisions

### D1 — Root base resolution order: explicit override → coordinator branch → project default

`resolveBaseBranch`, when it finds no blocker branch (a root node), resolves in this order:

1. The orchestrator's explicit `base_branch`, when set.
2. Otherwise the orchestrator's coordinator role's bound-task branch (the coordinator's own worktree branch).
3. Otherwise `""` — `CreateAndStart` then applies `projCfg.Branch` as today.

This keeps the change confined to the one function that already owns base resolution. Nodes with blockers never reach the new branch; their resolution is untouched.

### D2 — The explicit override lives on the orchestrator

The override is recorded once, at `hera_new_orchestrator` time, as a new nullable `base_branch` column on `hera_orchestrators` and a `HeraOrchestrator.BaseBranch` field. Chosen over a per-`hera_plan` or per-node base because the base is a property of the entire orchestration (one starting point for all roots), `hera_plan` can be called repeatedly, and a per-node root base is surface area no use case asked for (YAGNI).

### D3 — Default to the coordinator branch, not the project default

When no explicit override is set, roots follow the coordinator's branch rather than `projCfg.Branch`. A coordinator that has made no commits sits on a branch byte-identical to its base, so this is a no-op in the common case; a coordinator that HAS local commits (the WIP-feature-branch case this change exists for) now correctly seeds its plan on top of them. The stated backward-compat case — coordinator literally on `master` — yields `master`, unchanged.

## Risks / Trade-offs

- **Default behavior shift for coordinators on a non-default branch.** Today such a coordinator's roots go off `master`; now they go off the coordinator's branch. This is the intended fix and is the more correct behavior (roots build on the coordinator's actual context). The explicit-`master` case is preserved. Low risk: coordinators rarely carry divergent commits they would not want their plan based on.
- **Coordinator branch not resolvable** (coordinator task has no branch, or no coordinator role found). Falls through to `""` → `projCfg.Branch`, identical to today. No panic, no new failure mode.
- **Schema column add on a live DB.** Additive nullable column; mirrors how the substrate added `nuked_at`. No data migration; single-user breaking-changes policy applies.

## Migration Plan

- Additive: new nullable `base_branch` column on `hera_orchestrators`, defaulting empty. Existing orchestrators read back empty → fall to the coordinator-branch default. No backfill.
- Rollback: revert; the column is harmless if left, and `resolveBaseBranch` reverts to the `""`-for-roots behavior.

## Alternatives considered

- **Default off the project branch, explicit override only.** Simpler (no coordinator-branch lookup), but fails the core motivation — a coordinator on a WIP feature branch would still get roots off `master` unless it remembered to pass an override every time. Rejected: the ergonomic default is the point.
- **Per-`hera_plan` base branch.** More flexible but `hera_plan` is callable multiple times and the base is logically one value for the orchestration. Rejected as surface area without a use case.
- **Per-root-node base.** Maximum flexibility, maximum surface. No use case. Rejected (YAGNI).

## Discovery findings

- `resolveBaseBranch` (`heragater.go:306`) is the single owner of base resolution; it already iterates blockers and returns `""` when none resolve. The root fallback is a few lines at its tail.
- `materializeNode` (`heragater.go:276`) already looks up the coordinator via `ListHeraRolesByKind(orchID, HeraKindCoordinator)` — the same primitive the coordinator-branch fallback needs, so the lookup pattern already exists in this file.
- The coordinator's branch is reachable via `HeraLiveBindingByRole(coordRoleID)` → `db.Get(task).Branch`, the same chain `resolveBaseBranch` already uses for blockers.
- `CreateAndStart` applies `projCfg.Branch` only when `input.BaseBranch == ""` (`create.go:148-150`), so returning a non-empty branch from `resolveBaseBranch` is sufficient to override; no change needed in `CreateAndStart`.
- `HeraOrchestrator` (`hera.go:115`) and `CreateHeraOrchestrator(name string)` (`hera.go:191`) are the only schema touch-points; the `SELECT` at `hera.go:238` lists columns explicitly and must add `base_branch`.
- `hera_new_orchestrator` MCP handler is `toolHeraNewOrchestrator` (`mcp/hera.go:365`); the tool schema is declared at `mcp/hera.go:68`.

## Acceptance criteria

**Root base resolution**

- it should materialize a root node off the orchestrator's explicit base branch when one is set
- it should materialize a root node off the coordinator's branch when no explicit base is set
- it should fall back to the project default branch when neither an explicit base nor a coordinator branch is resolvable
- it should leave a blocker-having node's base resolution unchanged (most-recently-bound blocker branch)

**Authoring**

- it should accept and persist an optional base branch when bootstrapping an orchestrator
- it should default the persisted base branch to empty when none is supplied
