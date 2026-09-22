## 1. Worktree/branch primitive

- [x] 1.1 Add `branchNamespace` parameter to `CreateWorktree`; when non-empty, sanitize it independently and build the branch as `argus/<namespace>/<candidate>` instead of `argus/<candidate>`.
- [x] 1.2 Update all existing `CreateWorktree` call sites (production + tests) for the new parameter.
- [x] 1.3 Add `CreateInput.BranchNamespace` and thread it into `CreateWorktree` from `CreateAndStart`.

## 2. Hera spawn/materialize wiring

- [x] 2.1 Add a shared `resolveOrchestratorBranchNamespace` helper (fail-open DB lookup) in `internal/agent/hera_spawn.go`.
- [x] 2.2 Wire it into `SpawnHeraWorker` (namespace = `in.OrchestratorID`'s orchestrator name).
- [x] 2.3 Wire it into `MaterializeHeraWorker` (namespace = `in.Role.OrchestratorID`'s orchestrator name).
- [x] 2.4 Wire it into `MaterializeHeraSubCoordinator` (namespace = the PARENT `in.Role.OrchestratorID`'s orchestrator name, not the newly minted child).
- [x] 2.5 Confirm `SpawnHeraCoordinator` and plain `task_create`/TUI new-task paths are left untouched (flat branch preserved).

## 3. Tests (TDD)

- [x] 3.1 `worktree_test.go`: namespaced branch creation, independent sanitization of namespace vs. task-name segments, and the ref-namespace collision case (fails cleanly, no orphan state).
- [x] 3.2 `create_test.go`: `CreateInput.BranchNamespace` flows through `CreateAndStart` into `task.Branch`.
- [x] 3.3 `hera_spawn_test.go`: `SpawnHeraWorker` and `MaterializeHeraWorker` produce namespaced branches; existing unwind/failure tests still pass unmodified (fail-open behavior doesn't introduce a new failure mode).
- [x] 3.4 `hera_subcoord_test.go`: `MaterializeHeraSubCoordinator` namespaces under the parent orchestrator, not the child.

## 4. Verification, documentation, and archival

- [x] 4.1 Run `make pre-pr` clean (vuln gate's only failures are the pre-existing, documented advisory stdlib CVEs — CI runs this step with `continue-on-error`).
- [x] 4.2 Document the branch-namespacing invariant (and the accepted ref-collision failure mode) in `context/knowledge/gotchas/worktree.md`.
- [x] 4.3 Archive this OpenSpec change (merge deltas into base specs, move the change folder) in the same PR before merge.
