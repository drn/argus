## MODIFIED Requirements

### Requirement: Worktree creation with argus branch

The system SHALL create a git worktree at the deterministic path on a new branch named `argus/<task-name>`, basing it on the requested start point (defaulting to `HEAD` when no base branch is given). On success it SHALL return the worktree path, the final task name, and the branch name.

When no explicit base branch is given and the repository's checked-out `HEAD` is unborn (the repository has zero commits, so `HEAD` resolves to nothing), the system SHALL create the branch as an orphan (no start point) instead of attempting to base it on `HEAD`. This SHALL NOT apply when an explicit base branch is given: the system SHALL still resolve and honor an explicit base branch normally — including a branch that only exists because a different task already committed to it — even while the repository's own checked-out `HEAD` remains unborn.

The system SHALL accept an optional branch namespace. When a non-empty namespace is supplied, the branch SHALL instead be named `argus/<namespace>/<task-name>`, with the namespace sanitized independently from the task name (using the same sanitization rules as task names), so that a namespace containing a `/` or other invalid character cannot be mistaken for the deliberate separator between the namespace and task-name segments. When the namespace is empty, behavior is unchanged (the flat `argus/<task-name>` form).

#### Scenario: Fresh worktree created on a new branch

- **WHEN** a worktree is created for a task named "fix-bug" with no explicit base branch
- **THEN** the returned branch name is `argus/fix-bug`, the returned final name is "fix-bug", a `.git` entry exists inside the worktree directory, and the `argus/fix-bug` branch exists in the repository

#### Scenario: Worktree based on a custom start point

- **WHEN** a worktree is created with an explicit base branch that resolves to a valid commit
- **THEN** the worktree HEAD matches the commit of that base branch

#### Scenario: Worktree created in a repo with zero commits

- **WHEN** a worktree is requested with no explicit base branch, for a project whose repository has just been `git init`'d and has no commits (an unborn `HEAD`)
- **THEN** the worktree is created successfully as an orphan branch, a `.git` entry exists inside the worktree directory, and the `argus/<task-name>` branch has no commits yet

#### Scenario: Explicit base branch honored despite the project's own unborn HEAD

- **WHEN** a worktree is requested with an explicit base branch that resolves to a valid commit (e.g. a sibling task's already-committed `argus/<task>` branch), even though the project's own checked-out `HEAD` is unborn
- **THEN** the worktree is based on that resolved branch, not created as an orphan — its `HEAD` matches the commit of the explicit base branch

#### Scenario: Namespaced branch is created

- **WHEN** a worktree is created for task "cart-api" with branch namespace "checkout-revamp"
- **THEN** the returned branch name is `argus/checkout-revamp/cart-api`

#### Scenario: Namespace sanitized independently from the task name

- **WHEN** a worktree is created with a branch namespace containing a `/` or other invalid character (e.g. "team/a") and a task name of "c"
- **THEN** the namespace segment is sanitized on its own (e.g. to "team-a") before being joined with the task-name segment, producing `argus/team-a/c` — not an extra path segment that could be mistaken for nested namespacing

## ADDED Requirements

### Requirement: Branch namespace for hera-managed worker spawns

Hera worker spawn — both the ad-hoc spawn of a brand-new born-bound worker and the materialization of a pre-planned worker or subcoord node from a plan-DAG — SHALL pass the spawning orchestrator's name as the branch namespace, so the resulting branch is `argus/<orchestrator-name>/<role-name>` instead of `argus/<role-name>`. For a subcoord node (which materializes a new coordinator agent occupying a worker slot in its PARENT orchestrator's plan-DAG), the namespace SHALL be the PARENT orchestrator's name, not the newly minted child orchestrator's name.

Orchestrator-name resolution for the namespace SHALL fail open: if the orchestrator cannot be resolved, the spawn SHALL proceed with an empty namespace (the flat `argus/<role-name>` form) rather than aborting on that account alone.

Plain, non-hera task creation (`task_create`, the TUI's plain new-task flow) and root hera-coordinator spawns SHALL NOT be namespaced — they have no orchestrator to namespace a worker under, and continue to produce the flat `argus/<task-name>` branch unchanged.

#### Scenario: Ad-hoc worker spawn is namespaced under its orchestrator

- **WHEN** a coordinator in orchestrator "checkout-revamp" spawns a worker with role name "cart-api" via `hera_spawn_worker`
- **THEN** the resulting task's branch is `argus/checkout-revamp/cart-api`

#### Scenario: Plan-DAG materialized worker is namespaced under its orchestrator

- **WHEN** a plan-DAG node named "2b-impl" in orchestrator "my-orch" materializes into a live worker
- **THEN** the resulting task's branch is `argus/my-orch/2b-impl`

#### Scenario: Subcoord node is namespaced under the parent orchestrator, not the child

- **WHEN** a subcoord plan-DAG node named "3a-auth" in parent orchestrator "parent-orch" materializes (minting a new child orchestrator for its own team)
- **THEN** the resulting task's branch is `argus/parent-orch/3a-auth`, not namespaced under the newly minted child orchestrator

#### Scenario: Unresolvable orchestrator falls back to a flat branch

- **WHEN** a worker spawn's orchestrator id cannot be resolved to an orchestrator row
- **THEN** the spawn is not aborted solely for that reason, and if it otherwise succeeds the resulting branch uses the flat `argus/<role-name>` form

#### Scenario: Plain task creation is not namespaced

- **WHEN** a plain (non-hera) task is created via `task_create` or the TUI's new-task flow
- **THEN** the resulting branch remains the flat `argus/<task-name>` form, unaffected by this requirement

### Requirement: Ref-namespace collision surfaces as an ordinary creation failure

Because old-style flat hera branches (`argus/<role>`) and new namespaced branches (`argus/<orchestrator>/<role>`) can coexist in the same repository during and after this change, a git ref-namespace collision is possible: a ref cannot simultaneously be a leaf (e.g. `refs/heads/argus/foo`) and a directory prefix (e.g. `refs/heads/argus/foo/bar`). The system SHALL NOT attempt to detect or remediate this ahead of time. When such a collision occurs, worktree creation SHALL fail with an ordinary error (the same shape as any other git failure surfaced by this capability) and SHALL leave no partially-created worktree, branch, or database row behind.

#### Scenario: Existing flat branch blocks a new namespaced branch

- **WHEN** a branch `argus/foo` already exists and a new worktree is requested with branch namespace "foo" and task name "bar" (which would require creating `argus/foo/bar`)
- **THEN** worktree creation returns an error, and no worktree directory or branch is left behind for the failed attempt
