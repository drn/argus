## Context

All git-branch naming for tasks funnels through `agent.CreateWorktree` (`internal/agent/worktree.go:202`), called once from `agent.CreateAndStart` (`internal/agent/create.go:160`). Every hera worker spawn path — the ad-hoc `hera_spawn_worker` MCP tool (`internal/mcp/hera.go`, adapted by `daemon.heraSpawnWorker`), the native Hera view's rail `w` key (`internal/tui/heraactions.go`), and plan-DAG materialization (`agent.MaterializeHeraWorker` / `agent.MaterializeHeraSubCoordinator` in `internal/agent/hera_spawn.go`) — already converges on the single shared primitive `agent.SpawnHeraWorker` / `agent.CreateAndStart`, so the namespace only needs to be threaded through `CreateInput` once.

Worker role names are short slugs (`DeriveHeraWorkerName`, or a plan-DAG node's short-id name) that are unique **within an orchestrator** (`db.UniqueHeraRoleName`) but not globally. Two different orchestrations produce the same role name routinely (e.g. two coordinators each spawning a `write-specs` worker), and today both land as `argus/write-specs` on the same shared `origin`.

## Goals / Non-Goals

**Goals:**

- Namespace every hera-managed worker branch under its orchestrator name: `argus/<orchestrator>/<role>`.
- Cover both spawn paths that create a brand-new worker (`SpawnHeraWorker`) and both plan-DAG materialize paths (`MaterializeHeraWorker`, `MaterializeHeraSubCoordinator`).
- Keep plain solo tasks (no orchestrator) and root coordinator spawns (`SpawnHeraCoordinator`) on the existing flat `argus/<name>` — they're not "workers" and have no orchestrator to namespace under.
- Sanitize the namespace and role-name segments independently so an orchestrator name containing a slash (or other separator-like character) can't be confused with the deliberate `/` between the two segments.

**Non-Goals:**

- Pre-detecting or auto-remediating a git ref-namespace collision (a leaf ref like `argus/foo` blocking a new tree ref `argus/foo/bar`, or vice versa). This is accepted as a rare failure that surfaces through the existing `CreateWorktree` error path — see Decisions and Risks below.
- Renaming or migrating any already-existing branch. This only changes what NEW branches are named going forward (breaking changes are fine per this repo's conventions — no migration code).
- Namespacing plain `task_create` / TUI plain-new-task / `SpawnHeraCoordinator` branches. Explicitly out of scope per the proposal — only hera-managed *workers*.

## Decisions

**D1: Resolve the orchestrator name via a DB lookup inside the agent-layer spawn primitives, not by threading a new field through the MCP/daemon/heragater plumbing.**

`SpawnHeraWorker` already receives `OrchestratorID`; `MaterializeHeraWorker`/`MaterializeHeraSubCoordinator` already receive a `*db.HeraRole` carrying `OrchestratorID`. All three already have `*db.DB` in scope and `db.HeraOrchestrator(id) (*HeraOrchestrator, error)` already exists (used elsewhere, e.g. `heragater.go:518`). Resolving the name with one extra DB read inside these three functions means:

- No change to `mcp.HeraSpawnInput`, `internal/mcp/hera.go`, `daemon.heraSpawnWorker`, the `heragater.Materializer` function type, or `daemon.heraGaterMaterialize`/`heraGaterMaterializeSubCoord` — all of which the task brief flagged as *possibly* needing threading, but turn out not to.
- One shared helper (`resolveOrchestratorBranchNamespace`) covers all three call sites with identical fail-open semantics.

Alternative considered: add `OrchestratorName` to `HeraSpawnInput`/`HeraWorkerSpawnInput` and have the MCP layer (which already resolves `caller.orch.Name` for the orientation prefix) pass it through. Rejected — it touches four more files for no behavioral benefit over a local DB read, and the DB read is already paid for elsewhere in the same call path (e.g. `UniqueHeraRoleName`).

**D2: `MaterializeHeraSubCoordinator` namespaces under the PARENT orchestrator, not the child.**

A subcoord node occupies a worker-shaped slot in the PARENT orchestrator's plan-DAG — `in.Role.OrchestratorID` is the parent. The child orchestrator (and its `coord` role) is only created inside the `AfterPersist` hook, strictly after `CreateWorktree` already ran, so the child's name is not even available at worktree-creation time. Namespacing under the parent is both the only option and the semantically correct one: "this task occupies worker slot `<role>` in orchestrator `<parent>`."

**D3: Resolution fails open.**

If `database.HeraOrchestrator(id)` errors (not found, or any other lookup failure), the namespace is left empty and the branch falls back to the flat `argus/<role>` form — the spawn is NOT aborted on this account alone. Two reasons:

- A hera worker's `OrchestratorID` is FK-constrained at the role/binding write in `AfterPersist`, which runs *after* the worktree step. If the orchestrator id is genuinely bad, that write already fails and unwinds the worktree — the existing failure mode is preserved unchanged (see `TestSpawnHeraWorker_RoleBindingFailureUnwinds`, which passes an invalid `OrchestratorID: 999999` and expects a clean unwind, not a specific error message).
- A namespace lookup is a nice-to-have, not a correctness-load-bearing step; failing open here means a transient or already-superseded orchestrator id degrades to "no namespace" instead of blocking a legitimate spawn.

**D4: Independent per-segment sanitization, not sanitizing the joined string.**

`CreateWorktree` builds the branch as `"argus/" + sanitizeBranchName(namespace) + "/" + candidate` (`candidate` — the role/task name — is already sanitized earlier in the function). `sanitizeBranchName` strips `/` (among other characters), so an orchestrator name like `team/a` sanitizes to `team-a` as a single segment — it can never introduce an extra path segment that would look like a deliberately nested namespace. Sanitizing the pre-joined string `namespace + "/" + candidate` instead would have the same effect here (the algorithm already treats `/` uniformly), but sanitizing independently keeps the two inputs' provenance clear in the code and matches how `candidate` is already computed once, upstream, for the plain (non-namespaced) path.

**D5: `CreateInput.BranchNamespace` is a plain new field, `CreateWorktree` gains a new trailing parameter.**

`CreateWorktree` has exactly one production caller (`CreateAndStart`) — adding a 5th parameter is a mechanical, low-risk change. Per this repo's breaking-changes policy, all test call sites are updated directly rather than adding an overload or a variadic option.

## Risks / Trade-offs

- **[Git ref-namespace collision]** A ref cannot simultaneously be a leaf (`refs/heads/argus/foo`) and a directory prefix (`refs/heads/argus/foo/bar`). This can arise two ways: (a) an orchestrator is literally named the same as an existing flat branch's role segment (e.g. a plain solo task once created `argus/foo`, and later a hera orchestrator named `foo` spawns a worker role `bar`, producing `argus/foo/bar`); or (b) the reverse — an orchestrator's worker role name collides with an existing namespace segment. → **Mitigation: none beyond today's existing error handling.** `CreateWorktree`'s `git worktree add -b <branch> ...` fails, the fallback `git worktree add <dir> <branch>` (attach-to-existing) also fails (the branch was never created), and the function returns a normal `error` via `cleanGitOutput` — exactly the same shape as any other git failure it already surfaces (e.g. "branch already exists" style errors). Nothing is left partially created: the worktree step runs *before* the DB row is persisted in `CreateAndStart`, so a `CreateWorktree` failure means `CreateAndStart` returns an error with no task/DB/session side effects at all — there is nothing to unwind. A dedicated test (`TestCreateWorktree_BranchNamespaceCollision`) proves this fails cleanly rather than corrupting state. Auto-detecting or renaming around the collision ahead of time is explicitly out of scope (Non-Goals) — it's a rare, self-resolving-by-retry situation (pick a different orchestrator or role name) not worth the added complexity for a single-user tool.
- **[Orchestrator renamed or deleted between planning and materialization]** A plan-DAG node materializes potentially much later than it was authored; if the orchestrator were renamed in between, the branch would use the CURRENT name, not the one at authoring time. → Acceptable: orchestrators are not renamed in this codebase today (no rename tool exists), so this is theoretical. If one is deleted (`DeleteHeraOrchestrator`), it cascades the role away too (FK cascade), so there is no planned node left to materialize — the scenario cannot occur in practice.

## Migration Plan

Deploy normally — this only changes the branch name chosen for NEWLY created hera worker tasks from this point forward. No schema change, no data migration, no effect on already-running tasks or their existing branches (their `Branch` column is unchanged). Rollback is the prior daemon binary.
