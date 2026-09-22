## Why

Every hera-spawned worker's branch is built as `argus/<role-name>` — flat, with no reference to which orchestrator spawned it. Role names are short, freeform slugs (e.g. `1a-write-specs`, `cart-api`) that are only unique *within* their own orchestrator, so two unrelated orchestrations produce colliding or cryptic-looking branches on the shared `origin` remote, and nothing in the branch name says which team or feature it belongs to. As hera orchestrations multiply, this floods `origin` with flat, hard-to-attribute branches.

## What Changes

- Hera-spawned worker branches gain an orchestrator-name segment: `argus/<orchestrator-name>/<role-name>` instead of `argus/<role-name>`. Applies to both ad-hoc `hera_spawn_worker` calls and plan-DAG-materialized nodes (including subcoord nodes, namespaced under the parent orchestrator they occupy a slot in).
- Plain, non-hera solo tasks (`task_create`, the TUI's plain new-task flow, root hera coordinators) are **unchanged** — they keep today's flat `argus/<task-name>` branch. They have no orchestrator to namespace under.
- `internal/agent.CreateInput` gains an optional `BranchNamespace` field; when set, `CreateWorktree` builds the branch as `argus/<sanitized-namespace>/<sanitized-name>` instead of `argus/<sanitized-name>`. The namespace and role-name segments are sanitized independently so a slash embedded in an orchestrator name can't be mistaken for the deliberate namespace separator.
- Orchestrator-name resolution for the namespace fails open: if the orchestrator can't be resolved (e.g. a stale/invalid id), the spawn proceeds with a flat branch rather than aborting — the pre-existing failure paths (FK violation on the role/binding write, gater retry) are left to report any real problem.
- **Not handled**: a git ref-namespace collision (an existing 2-segment branch like `argus/foo` blocking a new 3-segment `argus/foo/bar`, or vice versa) is left to fail with git's own error, surfaced as an ordinary `CreateWorktree` failure. No pre-flight detection or automatic remediation is added — see design.md.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `worktree-management`: the branch-naming requirement gains the orchestrator-namespace form for hera-managed workers, `CreateInput` gains the namespace field threaded through transactional task creation, and a new requirement documents the accepted (unhandled) ref-namespace collision failure mode.

## Impact

`internal/agent/worktree.go` (`CreateWorktree`), `internal/agent/create.go` (`CreateInput`, `CreateAndStart`), `internal/agent/hera_spawn.go` (`SpawnHeraWorker`, `MaterializeHeraWorker`, `MaterializeHeraSubCoordinator`), their tests, and the worktree-management specification.
