# Tasks: fix coordinator self-promotion

**Design doc:** `openspec/changes/fix-coordinator-self-promotion/design.md`

## 1. Tests (failing first)

- [x] 1.1 In `internal/mcp/hera_test.go`, add a test: a caller task already holding a live coordinator binding under orchestrator A calls `hera_new_orchestrator(name=B)` → expect a tool error, AND assert no orchestrator B was created and no second coordinator binding exists on the task (proves the early-guard + no-orphan behavior).
- [x] 1.2 Add a test: a caller holding only a live worker binding calls `hera_new_orchestrator(name=B)` → expect success (worker-promotion), a new orchestrator B + coordinator role/binding on the caller.
- [x] 1.3 Add a test: a caller with no binding calls `hera_new_orchestrator(name=X)` → expect success (fresh bootstrap) — extend/confirm existing coverage.
- [x] 1.4 Assert the rejection error text steers to `hera_spawn_worker` / `kind=subcoord` (actionable-guidance criterion).
- [x] 1.5 Confirm 1.1/1.2/1.4 fail against current code (the guard does not yet exist), proving the gap.

## 2. Implement the coordinator self-invoke guard

**Depends on:** Stage 1

- [x] 2.1 In `toolHeraNewOrchestrator` (`internal/mcp/hera.go`), immediately after `resolveTask` and BEFORE `CreateHeraOrchestrator`, list the caller's live bindings (`ListHeraLiveBindingsByTask(task.ID)`) and reject with an actionable error if any resolves to a `HeraKindCoordinator` role (`HeraRole(binding.RoleID).Kind`).
- [x] 2.2 Keep the existing same-orchestrator guard (line ~456) as-is; the new guard is additive and runs earlier.
- [x] 2.3 Add a `uxlog`/`slog` line for the rejection (feature-consistent prefix) recording the caller task + existing coordinator orchestrator.
- [x] 2.4 Run the Stage 1 tests green.

## 3. Docs + archive

**Depends on:** Stage 2

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/orchestration.md`: the code guard (reject coordinator caller, early, actionable error) that backstops the `HeraCoordinatorOrientation` prose; bump the index bullet count if a new bullet is added.
- [x] 3.2 Run `make pre-pr` green (build/vet/fmt-check/lint-pr/test-cover-gate; vuln stdlib-advisory only).
- [x] 3.3 Archive this change within the PR: merge the delta into `openspec/specs/hera-coordination/spec.md` and move the change folder to `openspec/changes/archive/<date>-fix-coordinator-self-promotion/` (via `openspec archive` or the manual equivalent).
- [x] 3.4 Amend/commit onto the `argus/fix-coordinator-self` branch and force-push to update PR #835; refresh the PR description/comment with the guardrail + openspec rationale.
