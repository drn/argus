**Design doc:** openspec/changes/add-hera-plan-substrate/design.md

## 1. Tests (Prove-It)

- [ ] 1.1 Write failing tests from each scenario in `specs/task-orchestration/spec.md` (planned-node create, cycle reject, cross-orch reject, missing-blocker prune, materialize-on-unblock, idempotent materialize, stays-planned, worktree/base_branch-at-materialize, check-in order, go/wait-via-inbox, spawn-doesn't-wait, role-done gate, still-working-stays-planned, crash-holds-and-notifies, short-id accepted, short-id stable, sub-coord-is-one-parent-node, parent-level phase sequencing, independent sibling sub-teams, adopt-preserves-DAG)
- [ ] 1.2 Write failing tests from each MODIFIED scenario in `specs/hera-coordination/spec.md` (plan tools coordinator-only, whole-graph submit, materialization binds a pre-created role)
- [ ] 1.3 Confirm every `it should X` criterion in `design.md` has a failing test before any implementation (use `internal/testutil`; `t.Run` subtests; `t.Setenv("HOME", t.TempDir())` for any CreateAndStart/WorktreeDir path; `agent.NewRunner(nil)`; never touch real `~/.argus`)

## 2. DB layer — planned roles, edges, queries

**Depends on:** Stage 1

- [ ] 2.1 Add the `hera_blocks(blocked_role_id, blocker_role_id)` table to `internal/db/schema.go` with FKs to `hera_roles` (FK pragma on) and a uniqueness guard on the pair
- [ ] 2.2 Add a `CreateHeraRole`-without-binding store method (worker-kind role, no binding row), persisting project + delivery prompt + short-id-prefixed name
- [ ] 2.3 Add the blocking-edge insert with an in-transaction DFS cycle check and a same-orchestrator endpoint guard (reject cross-orchestrator)
- [ ] 2.4 Add queries: blockers-of-a-node, and planned-nodes-whose-blockers-all-have-role-status-`done` (treating missing blocker rows as satisfied)
- [ ] 2.5 Audit binding-keyed consumers (`heraadopt.ReconcileBindings`, subtree roll-up, rail) so a bindingless planned role is never ended, mangled, or mis-counted; add regression tests
- [ ] 2.6 Verify sub-coordinator composition emerges from the same-orchestrator edge guard: a parent-level blocking edge between two sub-coord worker roles (both in the parent orchestrator) is accepted, while each sub-coord's child-orchestrator DAG stays independent — no schema change, add tests

## 3. MCP authoring tools

**Depends on:** Stage 2

- [ ] 3.1 Add `hera_plan_node` to `internal/mcp/hera.go` (coordinator-only guard like `hera_spawn_worker`; creates a planned node via the Stage-2 store method)
- [ ] 3.2 Add `hera_block` (coordinator-only; adds a cycle-checked, single-orchestrator blocking edge; surfaces cycle/cross-orch rejections as tool errors)
- [ ] 3.3 Add `hera_plan` (coordinator-only; creates a whole graph of nodes + edges in one transactional call)
- [ ] 3.4 Update the native tool-surface registration/count (nine → twelve) and its dup-tool guard test so the count assertion matches

## 4. Materialization path + worker check-in

**Depends on:** Stage 2

- [ ] 4.1 Add a `CreateAndStart` path that binds + starts a **pre-created** planned role (reusing the existing `AfterPersist` + LIFO-cleanup machinery) instead of minting a fresh role; resolve worktree + `base_branch` from the node's blocker branches at this point
- [ ] 4.2 Build the check-in bootstrap prefix prepended to a materialized worker's prompt: instruct it to message its coordinator and **poll `hera_inbox`** for go/wait before real work (worker-pulled, never a passive push); add `uxlog` calls
- [ ] 4.3 Tests: materialization binds the existing role (no second role/agent); worktree + base_branch resolved at materialize; the delivered prompt carries the check-in order

## 5. The gater

**Depends on:** Stage 4

- [ ] 5.1 Add a hera-native gater package (mirroring the retired `internal/depswatcher`) that watches `hera_role_status` and finds planned nodes whose blockers have all reached role-status `done` (the worker's self-declaration — never argus task status)
- [ ] 5.2 Materialize eligible nodes via the Stage-4 path; make it idempotent (never materialize a node that is already bound/materializing/claimed)
- [ ] 5.3 Failure/hold: a blocker whose session ends without reaching role-status `done` holds the dependent (no materialize) and pings the coordinator over the bus; a still-`working` blocker simply keeps the dependent planned
- [ ] 5.4 Wire the gater into the daemon lifecycle (inert when no plan exists); `uxlog` materialize/hold/skip decisions

## 6. Docs + verification

**Depends on:** Stage 3, Stage 5

- [ ] 6.1 Document the non-obvious gotchas in `context/knowledge/gotchas/` (planned-role-without-binding invariant; idempotent materialize guard; worker-pulled-not-pushed check-in; missing-blocker prune; gate keys on hera role-status `done` NOT argus task status) — only the gotchas, not a feature description
- [ ] 6.2 Run `make pre-pr` and get a clean pass (build → vet → fmt-check → lint-pr → vuln → test-cover-gate)
- [ ] 6.3 Run `openspec validate add-hera-plan-substrate --strict` and confirm clean
