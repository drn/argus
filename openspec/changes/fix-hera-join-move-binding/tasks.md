**Design doc:** `openspec/changes/fix-hera-join-move-binding/design.md`

## 1. Tests

- [x] 1.1 In `internal/mcp/hera_test.go`, add `TestHera_Join_AttachMode_RejectsWhenBoundElsewhere`: caller already live-bound under orchestrator A calls `hera_join` attach mode targeting a different orchestrator B. Assert: the tool errors, the error directs the caller to `hera_move`, the binding under A remains live and unchanged, and no binding is created under B.
- [x] 1.2 Add `TestHera_Move_HappyPath`: caller live-bound under A calls `hera_move` targeting B with a role_name and kind. Assert: the binding under A now has `ended_at` set and `end_reason == "moved"`, a new live binding exists under B, and the response reports A + the moved role's name plus the new binding id.
- [x] 1.3 Add `TestHera_Move_NothingToMove`: an unbound task calls `hera_move`. Assert an error directing to `hera_join`/`hera_new_orchestrator`, and no binding created.
- [x] 1.4 Add `TestHera_Move_SameOrchestrator`: caller live-bound under A calls `hera_move` targeting A. Assert a no-op error directing to `hera_join` claim mode, and nothing ended or created.
- [x] 1.5 Add `TestHera_Move_AmbiguousRequiresFromOrchestrator`: caller with 2 live bindings (via `hera_new_orchestrator` self-promotion) calls `hera_move` without `from_orchestrator`. Assert the same disambiguation-error shape used elsewhere (listing bound orchestrator names); a follow-up call supplying `from_orchestrator` succeeds.
- [x] 1.6 Add `TestHera_Move_CoordinatorKindRejected`: mirrors `TestHera_Join_CoordinatorKindRejected` for `hera_move`.
- [x] 1.7 Confirm existing regression guards still pass unmodified: `TestHera_Join_AttachMode` (unbound caller happy path), `TestHera_Join_AlreadyBound` (same-orchestrator conflict), `TestHera_NewOrchestrator_WorkerSelfPromotionAllowed`.
- [x] 1.8 Run `make test-pkg PKG=./internal/mcp/` — new tests (1.1-1.6) fail (feature doesn't exist yet); regression guards (1.7) pass, proving no accidental breakage from the test additions alone.

## 2. DB layer: move-capable role+binding creation

**Depends on:** Stage 1

- [x] 2.1 In `internal/db/hera.go`, add a `MoveHeraBinding`-style function that, inside one `WithTx`: ends the caller's given live binding (`end_reason: "moved"`) and inserts the new role+binding under the target orchestrator — mirrors `CreateHeraRoleWithBinding`'s transactional pattern exactly, just with an extra end-binding step first.
- [x] 2.2 Return the ended binding's orchestrator name + role name alongside the new role/binding, so the MCP layer can report the move.
- [x] 2.3 Add DB-layer unit tests in `internal/db/hera_test.go` for the new function: happy-path move, and confirm the old binding is unreachable as "live" afterward (`ListHeraLiveBindingsByTask` no longer includes it).
- [x] 2.4 Run `make test-pkg PKG=./internal/db/`.

## 3. MCP layer: hera_join rejection + hera_move tool

**Depends on:** Stage 2

- [x] 3.1 In `internal/mcp/hera.go`, add the cross-orchestrator-bound rejection check to `toolHeraJoin`'s attach-mode branch (using `ListHeraLiveBindingsByTask` or equivalent) — before creating a new binding, error out with text directing to `hera_move` when the caller holds a live binding under a different orchestrator than the target.
- [x] 3.2 Add a new `toolHeraMove` handler: parse `cwd`, `orchestrator` (target), `role_name`, `kind`, optional `from_orchestrator`, optional `status`; resolve the caller's current binding via the existing disambiguation-capable resolver (passing `from_orchestrator` where that resolver expects an orchestrator hint); reject `kind=coordinator`; reject "nothing to move" (no live binding) and "same orchestrator" (source == target) cases with the errors specified in the delta spec; otherwise call the Stage 2 DB function and build the success response.
- [x] 3.3 Register `hera_move` in the tool list (name, JSON schema, required args `cwd`/`orchestrator`/`role_name`/`kind`, optional `from_orchestrator`/`status`) alongside the existing `hera_*` tools; update the dup-tool guard's suppressed-tool list if it enumerates tool names explicitly (so the plugin's `hera_move`-equivalent, if any, is still suppressed correctly). *(dup-tool guard is scope-based (`Scope == "hera"`), not name-enumerated — `hera_move` is automatically covered via `heraToolDefs`, no separate list to update.)*
- [x] 3.4 Run `make test-pkg PKG=./internal/mcp/` — all of Stage 1's new tests pass; regression guards remain green.

## 4. Docs

**Depends on:** Stage 3

- [x] 4.1 Add a short bullet to the hera schema/store entry in `context/knowledge/gotchas/orchestration.md` documenting: `hera_join` now rejects+redirects when the caller is bound elsewhere, the new `hera_move` tool, and that `hera_new_orchestrator`'s self-promotion path remains the only way to hold 2+ live bindings.
- [x] 4.2 Update the README Reference MCP tools table to add `hera_move` (per this repo's convention of keeping that table factually current for any MCP tool addition).

## 5. One-off data cleanup

**Depends on:** none (independent of the code change; safe to run any time)

- [ ] 5.1 Write and run a one-off script (not a reusable migration, per this repo's breaking-changes policy) that ends the specific stray binding found during investigation — `hera_bindings.id=596` (role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`/id 66, `argus_task_id` `1783320494974244000`) — stamping `ended_at`/`end_reason` (e.g. `manual_cleanup`).
- [ ] 5.2 Verify via the TUI (or a direct DB query) that `11a-archive` no longer appears under the Freelance section and still appears correctly as a worker under `coordctx-exec`.

## 6. Verification

**Depends on:** Stage 3, Stage 5

- [ ] 6.1 `make pre-pr` passes clean.
- [ ] 6.2 Confirm every acceptance criterion listed in `design.md` has a corresponding passing test.
