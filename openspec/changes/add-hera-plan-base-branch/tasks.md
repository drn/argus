**Design doc:** `openspec/changes/add-hera-plan-base-branch/design.md`

## 1. Tests (Prove-It)

- [x] 1.1 Failing test: a root planned node materializes off the orchestrator's explicit `base_branch` when set (gater fixture; assert the base passed to the materializer)
- [x] 1.2 Failing test: with no explicit base, a root node materializes off the coordinator role's bound-task branch
- [x] 1.3 Failing test: with neither explicit base nor resolvable coordinator branch, `resolveBaseBranch` returns `""` (so `CreateAndStart` applies the project default) — backward-compat case incl. coordinator on the default branch
- [x] 1.4 Failing test: a blocker-having node's base resolution is unchanged (most-recently-bound blocker branch) — regression guard
- [x] 1.5 Failing test: `CreateHeraOrchestrator` persists a supplied `base_branch` and defaults it to empty when none supplied; `HeraOrchestrator` round-trips the field
- [x] 1.6 Confirm every `it should X` criterion in the delta has a failing test before implementing

## 2. Persistence: orchestrator base branch

**Depends on:** Stage 1

- [x] 2.1 Add nullable `base_branch` column to the `hera_orchestrators` schema + a migration consistent with how `nuked_at` was added (additive, single-user policy)
- [x] 2.2 Add `BaseBranch string` to `HeraOrchestrator` (`internal/db/hera.go`); include `base_branch` in the `SELECT` at the list/get paths
- [x] 2.3 Change `CreateHeraOrchestrator(name string)` → `CreateHeraOrchestrator(name, baseBranch string)`; INSERT the column; update all callers

## 3. Gater: root base resolution

**Depends on:** Stage 1, Stage 2

- [x] 3.1 In `resolveBaseBranch` (`internal/heragater/heragater.go`), when no blocker branch resolves, fall back to: orchestrator `BaseBranch` if set → coordinator role's bound-task branch → `""`
- [x] 3.2 Resolve the coordinator branch via `ListHeraRolesByKind(orchID, HeraKindCoordinator)` → `HeraLiveBindingByRole` → `db.Get(task).Branch`; degrade to `""` on any miss (no panic)
- [x] 3.3 uxlog/slog the resolved root base and which source won (explicit / coordinator / default), consistent `[heragater]` prefix

## 4. MCP authoring surface

**Depends on:** Stage 2

- [x] 4.1 Add optional `base_branch` to the `hera_new_orchestrator` tool schema (`internal/mcp/hera.go`)
- [x] 4.2 Parse it in `toolHeraNewOrchestrator` and pass through to `CreateHeraOrchestrator`; update the `hera` skill / gotchas note if the tool surface doc lists params (README/skill list tools by name only, no per-param table — no edit needed per "default to silence")
- [x] 4.3 Update `context/knowledge/gotchas/orchestration.md` for the root-base resolution order

## 5. Verify

**Depends on:** Stage 2, Stage 3, Stage 4

- [x] 5.1 `PATH="$HOME/go/bin:$PATH" GIT_CONFIG_GLOBAL=/dev/null make pre-pr` — build/vet/fmt-check/lint-pr all green; this change's own tests (heragater + db hera-orchestrator) all pass. NOTE: `test-cover-gate` is RED on this branch due to the parallel in-flight `add-hera-plan-view` change's deliberate Stage-2..7 stubs (`internal/db` `TestListHeraBlocks_*`, all of `internal/tui/planview`, and `internal/tui/hera` plan-node tests) — confirmed identical failures on the pristine base via `git stash`. No failure is introduced by this change.
