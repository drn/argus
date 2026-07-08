**Design doc:** `openspec/changes/fix-hera-join-move-binding/design.md`

## 1. Tests

- [ ] 1.1 In `internal/mcp/hera_test.go`, add `TestHera_Join_AttachMode_MovesExistingBinding`: caller already live-bound (worker or freelance) under orchestrator A calls `hera_join` attach mode targeting a different orchestrator B without `keep_existing`. Assert: the binding under A now has `ended_at` set and `end_reason == "moved"`, a new live binding exists under B, and the response text reports orchestrator A + the moved-from role name.
- [ ] 1.2 Add `TestHera_Join_AttachMode_KeepExisting`: same setup as 1.1 but with `keep_existing: true`. Assert both bindings (A and B) remain live and the response does not report anything as moved.
- [ ] 1.3 Add `TestHera_Join_AttachMode_UnboundCaller_NoMove`: confirm `TestHera_Join_AttachMode` (existing, unbound-caller happy path) still passes unmodified — an unbound caller creating its first binding has nothing to move and the response has no moved-from info.
- [ ] 1.4 Confirm `TestHera_Join_AlreadyBound` (existing same-orchestrator conflict test) still passes unmodified — no binding is ended or created on that path.
- [ ] 1.5 Confirm `TestHera_NewOrchestrator_WorkerSelfPromotionAllowed` (existing) still passes unmodified — `hera_new_orchestrator`'s code path is untouched by this change.
- [ ] 1.6 Run `make test-pkg PKG=./internal/mcp/` — 1.1 and 1.2 fail (feature doesn't exist yet), 1.3-1.5 pass (proving no regression from the test additions alone).

## 2. DB layer: move-capable role+binding creation

**Depends on:** Stage 1

- [ ] 2.1 In `internal/db/hera.go`, add a DB-layer entry point (e.g. extend `CreateHeraRoleWithBinding` with a `moveFromOtherOrchestrators bool` param, or add a sibling function) that, inside one `WithTx`: fetches the calling task's current live binding(s) via `ListHeraLiveBindingsByTask` joined to role name + orchestrator name, ends each with `EndHeraBinding`-equivalent logic (`end_reason: "moved"`) when the move flag is set, then inserts the new role+binding exactly as `CreateHeraRoleWithBinding` does today.
- [ ] 2.2 Return the ended binding's orchestrator name + role name (if any) alongside the new role/binding, so the MCP layer can report it.
- [ ] 2.3 Add/extend DB-layer unit tests in `internal/db/hera_test.go` for the new function: moves-by-default case, keep-existing-skip case, and the zero-prior-bindings case (no-op move step).
- [ ] 2.4 Run `make test-pkg PKG=./internal/db/`.

## 3. MCP layer: hera_join attach mode

**Depends on:** Stage 2

- [ ] 3.1 In `internal/mcp/hera.go`, add `KeepExisting bool `json:"keep_existing"`` to `toolHeraJoin`'s attach-mode param struct, and document the new optional param in the tool's JSON schema description (`internal/mcp/hera.go` tool registration, mirroring how `kind` is documented).
- [ ] 3.2 Wire the attach-mode branch to call the new move-capable DB function from Stage 2 instead of plain `CreateHeraRoleWithBinding`, passing `!p.KeepExisting` as the move flag.
- [ ] 3.3 Update the attach-mode success response text to include the moved-from orchestrator + role name when a prior binding was ended (mirroring the existing `**binding_id**:` line style).
- [ ] 3.4 Run `make test-pkg PKG=./internal/mcp/` — all of Stage 1's tests pass (1.1 and 1.2 now green).

## 4. Docs

**Depends on:** Stage 3

- [ ] 4.1 Add a short bullet to the hera schema/store entry in `context/knowledge/gotchas/orchestration.md` documenting move-by-default on `hera_join` attach mode and the `keep_existing` override, cross-referencing that `hera_new_orchestrator`'s self-promotion multi-binding path is unaffected.

## 5. One-off data cleanup

**Depends on:** none (independent of the code change; safe to run any time)

- [ ] 5.1 Write and run a one-off script (not a reusable migration, per this repo's breaking-changes policy) that ends the specific stray binding found during investigation — `hera_bindings.id=596` (role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`/id 66, `argus_task_id` `1783320494974244000`) — stamping `ended_at`/`end_reason` (e.g. `manual_cleanup`).
- [ ] 5.2 Verify via the TUI (or a direct DB query) that `11a-archive` no longer appears under the Freelance section and still appears correctly as a worker under `coordctx-exec`.

## 6. Verification

**Depends on:** Stage 3, Stage 5

- [ ] 6.1 `make pre-pr` passes clean.
- [ ] 6.2 Confirm every acceptance criterion listed in `design.md` has a corresponding passing test.
