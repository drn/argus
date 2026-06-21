## Why

The hera plan-DAG gater resolves a materializing node's `base_branch` from its blockers' branches, but a root node (no blockers) always falls back to the project default branch (`master`). There is no way to seed a whole plan-DAG onto a different base, so a plan cannot stack on unmerged work-in-progress.

## What Changes

- **Root nodes inherit the coordinator's branch by default.** When `resolveBaseBranch` finds no blocker branch, it falls back to the orchestrator's coordinator role's bound-task branch instead of `""`. A DAG starts wherever the coordinator sits. Backward-compatible: a coordinator on `master` still yields roots off `master`.
- **Optional explicit base branch on the orchestrator.** `hera_new_orchestrator` accepts an optional `base_branch`, persisted on `hera_orchestrators`. When set, root nodes use it instead of the coordinator branch — the "start off anything" override.
- **Final fallback unchanged.** When neither an explicit base nor a coordinator branch resolves, `CreateAndStart` applies the project default branch as today.
- Non-root (blocker-having) base resolution and fan-in behavior are unchanged.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `task-orchestration`: adds a requirement for configurable root-node base-branch resolution (explicit override → coordinator branch → project default). The substrate's gater-materialization requirement (which specifies base resolution for nodes with blockers) is unchanged; this composes with it.

## Impact

- **Code:** `internal/heragater/heragater.go` (`resolveBaseBranch` root fallback); `internal/db/hera.go` (`HeraOrchestrator.BaseBranch` field, `hera_orchestrators.base_branch` column, `CreateHeraOrchestrator` signature + `SELECT`); `internal/mcp/hera.go` (`hera_new_orchestrator` optional `base_branch` arg + handler).
- **Specs:** `task-orchestration` delta.
- **Schema:** additive nullable `base_branch` column on `hera_orchestrators`; no data migration (single-user breaking-changes policy).
- **No TUI/API/web impact:** substrate only.
- **Depends on** `add-hera-plan-substrate` (the gater + `resolveBaseBranch` it extends).
