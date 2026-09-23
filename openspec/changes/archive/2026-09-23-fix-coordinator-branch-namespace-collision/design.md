## Context

`add-branch-namespacing` (archived 2026-09-22, `openspec/changes/archive/2026-09-22-namespace-hera-worker-branches/`) namespaced every hera *worker* branch under its spawning orchestrator's name via `resolveOrchestratorBranchNamespace` + `CreateInput.BranchNamespace`, threaded through `SpawnHeraWorker`, `MaterializeHeraWorker`, and `MaterializeHeraSubCoordinator` (parent-namespaced). It explicitly left `SpawnHeraCoordinator` (`internal/agent/hera_spawn.go:453`) alone, keeping its branch flat: `argus/<orchestrator-name>` (since `taskName` there defaults to `orchName` when `in.TaskName` is empty — the only caller, `heraDoNewCoordinator` in `internal/tui/heraactions.go`, always leaves `TaskName` blank).

A git ref cannot simultaneously be a leaf (`refs/heads/argus/foo`) and a directory prefix (`refs/heads/argus/foo/bar`). Because the coordinator's branch is *always* `argus/<orchestrator>` and a worker's branch is *always* `argus/<orchestrator>/<role>`, the two forms collide on the exact same orchestrator name every time — not a coincidental name clash between two independently-chosen strings, but a structural guarantee. The original design's Risks section treated this as a rare, self-resolving case; it is not, for this specific path. Confirmed live: `hera_spawn_worker` under orchestrator "argus-folders" failed with `fatal: cannot lock ref 'refs/heads/argus/argus-folders/<role>': 'refs/heads/argus/argus-folders' exists` on the very first worker spawn.

## Goals / Non-Goals

**Goals:**

- Namespace `SpawnHeraCoordinator`'s own branch under its own orchestrator name, the same namespace its workers already use, so the orchestrator name is *never* a leaf ref — only ever a directory prefix.
- Guarantee the coordinator's branch leaf is textually distinct from the namespace segment, so `argus/<orchestrator>` (the old flat form) is never created and can never block a later worker spawn.
- Correct the base spec's requirement, which explicitly says root coordinator spawns stay flat.

**Non-Goals:**

- Changing `MaterializeHeraSubCoordinator`'s namespacing (parent-orchestrator, freeform planned-role leaf name) — verified not exposed to this same *guaranteed* collision (see Decisions, D2).
- Pre-detecting or auto-remediating ref-namespace collisions in general (still out of scope per the original design's Non-Goals; this change removes one *guaranteed* instance of the collision, it does not add general collision detection).
- Decoupling a task's branch leaf from its display `Name`/worktree-directory name inside `CreateWorktree`/`CreateInput` — no such mechanism exists today (the same `Name` value drives both `finalName` and the branch candidate), and adding one is more machinery than this bug needs (see D1).
- Physical worktree directory placement (`WorktreeDir`) — untouched; there is no filesystem git-in-git nesting today, confirmed by inspection, and none is introduced here.

## Decisions

**D1: Change the coordinator's default task-name derivation instead of adding a separate branch-leaf field.**

`CreateWorktree`'s `candidate` (the branch leaf, sanitized) and `finalName` (the task's persisted `Name`, used for both display and the worktree directory) are the same value — there is exactly one `Name` in `CreateInput`, not a task name and a separate branch name. Reusing `orchName` as `BranchNamespace` while leaving the default `taskName` at `orchName` would reproduce the exact bug one level down (`argus/<orch>/<orch>` — no longer colliding with anything, since it's ALREADY namespaced, but redundant and confusing). Instead, `SpawnHeraCoordinator` now defaults `taskName` (when `in.TaskName` is blank, true for every current caller) to `coordName + "-" + orchName` (e.g. `"coord-ship-feature"`) rather than bare `orchName`. This:

- Guarantees the leaf can never equal the namespace (`coordName` is never empty — it defaults to `"coord"` — so the joined string always differs from `orchName` alone).
- Keeps the orchestrator identifiable from the task name at a glance (it's still a suffix), a softer version of the previous "task name == orchestrator name" property the `heraactions.go` comment described.
- Needs no change to `CreateWorktree`, `CreateInput`, or any other spawn path — the fix is entirely local to `SpawnHeraCoordinator`'s own default.

Alternative considered: add a distinct `BranchLeaf`/`BranchName` field to `CreateInput`, decoupled from `Name`, so the task's display name could stay exactly `orchName` while the branch used something else. Rejected: no other caller needs this decoupling, `CreateWorktree` has exactly one production caller today (`add-branch-namespacing` D5), and introducing a second identity for "what this task is called" purely to preserve an incidental naming symmetry is more surface area than the bug warrants.

**D2: `MaterializeHeraSubCoordinator` is unaffected — verified, not assumed.**

A subcoord node's branch is namespaced under the PARENT orchestrator (`in.Role.OrchestratorID`), and its leaf is the pre-created **planned role's own name** (e.g. `"3a-auth"`, a freeform short-id slug the plan author chose) — never a value that *defaults to the orchestrator's own name*. The structural guarantee that makes the root-coordinator case a 100% reproducible collision (task name defaults to orchestrator name, always) simply does not exist on this path: a planned subcoord role would need to coincidentally share its parent orchestrator's exact name to collide, which is the same class of rare, accepted coincidental-collision risk the original design already carries for sibling workers. No code change needed here; `TestSubCoord_MaterializeBranchNamespacedUnderParentOrchestrator` (pre-existing, passing) already pins the parent-namespaced leaf-is-role-name behavior this reasoning depends on.

**D3: Resolve the namespace from the already-in-hand `orchName` string, not via `resolveOrchestratorBranchNamespace`.**

`SpawnHeraCoordinator` already holds `orchName` as a plain string (it just minted the orchestrator via `CreateHeraOrchestrator(orchName, "")` a few lines earlier) — there is no ID-only handle here the way `SpawnHeraWorker`/`MaterializeHeraWorker` have. Calling `resolveOrchestratorBranchNamespace(database, orch.ID)` would be a redundant DB round-trip for a value already available; `BranchNamespace: orchName` is set directly.

## Risks / Trade-offs

- **[Coincidental leaf/leaf collision]** A worker whose own uniquified role name happens to literally equal the coordinator's derived leaf (`coordName + "-" + orchName`) would collide with the coordinator's own branch. This is the same *coincidental* collision class the original design already accepts for sibling workers (two workers, or a worker and a differently-named coordinator, choosing the same string) — not the guaranteed structural collision this change fixes. No new mitigation added, consistent with the base spec's "Ref-namespace collision surfaces as an ordinary creation failure" requirement.
- **[Coordinator task display name changes]** Every newly-spawned root coordinator's task `Name` changes from bare `orchName` (e.g. `"ship-feature"`) to `coordName + "-" + orchName` (e.g. `"coord-ship-feature"`). This is a visible, deliberate behavior change to the plain Tasks-tab display name, not just an internal branch-naming detail — accepted per this repo's breaking-changes policy (single user, no back-compat). Existing already-running coordinators' tasks/branches are unaffected (this only changes what's chosen for newly created tasks).

## Migration Plan

Deploy normally — this only changes the task name and branch chosen for NEWLY created root-coordinator spawns going forward. No schema change, no data migration, no effect on already-running coordinator tasks or their existing branches. Rollback is the prior daemon binary.
