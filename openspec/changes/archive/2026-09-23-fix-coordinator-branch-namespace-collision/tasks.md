## 1. Implementation

- [x] 1.1 In `internal/agent/hera_spawn.go`, change `SpawnHeraCoordinator`'s default `taskName` derivation (when `in.TaskName` is blank) from bare `orchName` to `coordName + "-" + orchName`.
- [x] 1.2 In the same function, set `BranchNamespace: orchName` on the `CreateInput` passed to `CreateAndStart`.
- [x] 1.3 Update the stale comment in `internal/tui/heraactions.go` (`heraDoNewCoordinator`) describing the old "TaskName defaults to the bare orchestrator name" behavior.

## 2. Tests

- [x] 2.1 Update `TestSpawnHeraCoordinator_HappyPath` (`internal/agent/hera_spawn_test.go`) to assert the new default task name and add a branch assertion (`argus/<orch>/<coordName>-<orch>`).
- [x] 2.2 Add a coordinator-specific regression test proving the old flat-coordinator-branch + first-worker-spawn sequence no longer collides: spawn a coordinator via `SpawnHeraCoordinator`, then spawn a worker under the same orchestrator via `SpawnHeraWorker`, and assert both succeed with distinct, non-colliding branches.
- [x] 2.3 Confirm (add a short comment or assertion if not already covered) that `TestSubCoord_MaterializeBranchNamespacedUnderParentOrchestrator` demonstrates the sub-coordinator path is unaffected (parent-namespaced, freeform leaf) — no code change expected there.

## 3. Documentation

- [x] 3.1 Add a one-line gotcha to `context/knowledge/gotchas/worktree.md` documenting the invariant: an orchestrator name must never double as both a namespace prefix and a leaf ref.

## 4. Verification & Ship

- [x] 4.1 Run `make pre-pr` clean.
- [x] 4.2 Archive this change (`openspec archive fix-coordinator-branch-namespace-collision`) in the same PR, before merge.
- [ ] 4.3 Open the PR via `mcp__argus__iris_gh_pr_create`.
- [ ] 4.4 Report status to coordinator task `1790135658207847000` via `task_message_send`/`task_ask`.
