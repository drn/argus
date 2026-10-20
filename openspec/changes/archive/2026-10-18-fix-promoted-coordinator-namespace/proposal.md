## Why

A plain task on `argus/exec-irl-prep` can promote itself to an orchestrator named `exec-irl-prep`, after which every worker fails because Git cannot create children beneath an existing leaf ref. The earlier fresh-coordinator fix does not cover existing task branches.

## What changes

- Resolve Hera worker namespaces against local heads in the target source repository before spawning.
- Select deterministic, length-safe fallback namespaces when a leaf blocks the ordinary namespace, including collisions at fallback names.
- Cover ad-hoc workers, planned workers, and planned subcoordinators, including workers promoted to coordinators.
- Preserve existing task branches and fail-open namespace lookup behavior; log resolution and probe failures.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `worktree-management`: resolve blocking namespace leaves before Hera worker/materialization creation while retaining ordinary low-level worktree failure handling.

## Impact

Shared agent-layer spawning, regression tests, worktree gotchas. No API or wire-contract changes: TUI, web, and macOS observe the resulting task branch through existing fields. No frontend implementation or follow-up is needed.
