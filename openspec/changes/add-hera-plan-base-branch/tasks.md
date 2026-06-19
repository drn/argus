**Design doc:** `openspec/changes/add-hera-plan-base-branch/design.md`

## 1. Tests (Prove-It)

- [ ] 1.1 Failing test: a root planned node materializes off the orchestrator's explicit `base_branch` when set (gater fixture; assert the base passed to the materializer)
- [ ] 1.2 Failing test: with no explicit base, a root node materializes off the coordinator role's bound-task branch
- [ ] 1.3 Failing test: with neither explicit base nor resolvable coordinator branch, `resolveBaseBranch` returns `""` (so `CreateAndStart` applies the project default) — backward-compat case incl. coordinator on the default branch
- [ ] 1.4 Failing test: a blocker-having node's base resolution is unchanged (most-recently-bound blocker branch) — regression guard
- [ ] 1.5 Failing test: `CreateHeraOrchestrator` persists a supplied `base_branch` and defaults it to empty when none supplied; `HeraOrchestrator` round-trips the field
- [ ] 1.6 Confirm every `it should X` criterion in the delta has a failing test before implementing

## 2. Persistence: orchestrator base branch

**Depends on:** Stage 1

- [ ] 2.1 Add nullable `base_branch` column to the `hera_orchestrators` schema + a migration consistent with how `nuked_at` was added (additive, single-user policy)
- [ ] 2.2 Add `BaseBranch string` to `HeraOrchestrator` (`internal/db/hera.go`); include `base_branch` in the `SELECT` at the list/get paths
- [ ] 2.3 Change `CreateHeraOrchestrator(name string)` → `CreateHeraOrchestrator(name, baseBranch string)`; INSERT the column; update all callers

## 3. Gater: root base resolution

**Depends on:** Stage 1, Stage 2

- [ ] 3.1 In `resolveBaseBranch` (`internal/heragater/heragater.go`), when no blocker branch resolves, fall back to: orchestrator `BaseBranch` if set → coordinator role's bound-task branch → `""`
- [ ] 3.2 Resolve the coordinator branch via `ListHeraRolesByKind(orchID, HeraKindCoordinator)` → `HeraLiveBindingByRole` → `db.Get(task).Branch`; degrade to `""` on any miss (no panic)
- [ ] 3.3 uxlog/slog the resolved root base and which source won (explicit / coordinator / default), consistent `[heragater]` prefix

## 4. MCP authoring surface

**Depends on:** Stage 2

- [ ] 4.1 Add optional `base_branch` to the `hera_new_orchestrator` tool schema (`internal/mcp/hera.go`)
- [ ] 4.2 Parse it in `toolHeraNewOrchestrator` and pass through to `CreateHeraOrchestrator`; update the `hera` skill / gotchas note if the tool surface doc lists params
- [ ] 4.3 Update `context/knowledge/gotchas/orchestration.md` for the root-base resolution order

## 5. Verify

**Depends on:** Stage 2, Stage 3, Stage 4

- [ ] 5.1 `PATH="$HOME/go/bin:$PATH" GIT_CONFIG_GLOBAL=/dev/null make pre-pr` green (vuln stdlib-only OK; run `make test-cover-gate` separately if vuln short-circuits)
