**Design doc:** `openspec/changes/fix-bug-059-binding-worktree-collision/design.md`

## 1. DB layer — worktree-keyed lookup

- [x] 1.1 In `internal/db/hera.go`, add `HeraLiveBindingByWorktreeAndOrchestrator(worktreePath string, orchID int64) (*HeraBinding, error)` — the orchestrator-scoped twin of `HeraLiveBindingByTaskAndOrchestrator`, mapping onto `idx_hera_bindings_live_worktree_orch`.
- [x] 1.2 Add DB-level tests in `internal/db/hera_bindings_worktree_test.go`: happy path, not-found, orchestrator-scoped (a binding under a different orchestrator at the same worktree must not resolve), excludes-ended, multi-orchestrator (does not ambiguate, unlike the existing unscoped `HeraLiveBindingByWorktree`), and a DAO-level reproduction of the claim-vs-attach paradox (task-keyed miss for a colliding task id + a real UNIQUE-constraint reject on INSERT for that colliding task at the same worktree+orchestrator, then the worktree-keyed lookup reconciling to the original binding).
- [x] 1.3 Run `make test-pkg PKG=./internal/db/`.

## 2. Resolver — cwd disambiguation

**Depends on:** none (independent of Stage 1)

- [x] 2.1 In `internal/mcp/server.go`, rewrite `resolveTask`'s worktree-matching loop to collect ALL tasks tied for the longest matching prefix (not just the first), then disambiguate via a new `disambiguateCwdMatches` helper: one match → unchanged; drop archived; one non-archived left → return it; else prefer the single `in_progress` match; else refuse.
- [x] 2.2 Add `CwdAmbiguousError` (lists the candidate tasks + status) returned by `disambiguateCwdMatches` on genuine ambiguity.
- [x] 2.3 Add resolver-level tests in `internal/mcp/resolve_cwd_test.go`: prefers in_progress over stale archived, refuses two-in_progress, all-archived is unknown, single-match unchanged, two-archived-one-active resolves the active one, two-active-neither-in_progress is still ambiguous, and unrelated same-length worktrees are not mistaken for a tie.
- [x] 2.4 Run `make test-pkg PKG=./internal/mcp/`.

## 3. Handlers — task-then-worktree fallback + collision guards

**Depends on:** Stage 1, Stage 2

- [x] 3.1 In `internal/mcp/hera.go`, add `liveBindingForOrch` (task-keyed first, worktree-keyed fallback via the Stage 1 method) and `liveBindingForTask` (task-keyed first, worktree-keyed fallback via the existing unscoped `HeraLiveBindingByWorktree`) helpers; route `resolveCallerRole`'s both branches (with and without an explicit `orchestrator`) through them.
- [x] 3.2 Add the worktree-keyed pre-check to `toolHeraJoin`'s attach-mode branch: on a collision, return an actionable message (claim it via `hera_join`, or `hera_rebind` when the existing binding's `argus_task_id` differs from the caller's) instead of letting the INSERT surface a raw constraint error.
- [x] 3.3 Add the same worktree-keyed pre-check to `toolHeraNewOrchestrator`'s bootstrap guard.
- [x] 3.4 Add handler-level tests in `internal/mcp/hera_rebind_test.go`: `hera_join` claim resolves the live task despite a stale-archived worktree collision, claim resolves via the worktree fallback when the binding's own task id has drifted, attach returns the friendly collision message (never a raw UNIQUE error), claim surfaces the resolver's ambiguous-cwd refusal, and `hera_new_orchestrator` catches a drifted worktree collision with the same friendly message.
- [x] 3.5 Run `make test-pkg PKG=./internal/mcp/`.

## 4. `hera_rebind` repair verb

**Depends on:** Stage 3

- [x] 4.1 Add `RoleID`/worktree candidate gathering (`heraRebindCandidates`) and keeper-role selection (`pickHeraRebindKeeper`, mirroring `hera_move`'s `from_orchestrator` disambiguation pattern) to `internal/mcp/hera.go`.
- [x] 4.2 Add `toolHeraRebind`: resolves the caller's real live task, unions task-keyed + worktree-keyed live bindings under the named orchestrator, picks the keeper role (explicit `role_name` or the sole represented role), no-ops when already consistent, refuses when a different role holds a target slot, otherwise ends the stale binding and creates a clean one under the same role (preserving prompt/messages/status) and mirrors `meta:hera.role`.
- [x] 4.3 Register `hera_rebind` in `heraToolDefs` + the `tools/call` dispatch switch in `internal/mcp/server.go`; update the tool-count assertion (`TestToolsList_HeraOn` in `internal/mcp/hera_test.go`) from 16 to 17 tools.
- [x] 4.4 Add full `hera_rebind` coverage in `internal/mcp/hera_rebind_test.go`: happy repair (post-state agreement on both lookup paths, old binding ended, role's message survives, `meta:hera.role` mirrored), no-op when already consistent, explicit `role_name` selection, and the refusal cases (ambiguous cwd, multiple roles with no `role_name`, nothing to reconcile, unknown orchestrator).
- [x] 4.5 Run `make test-pkg PKG=./internal/mcp/`.

## 5. Docs

**Depends on:** Stage 4

- [x] 5.1 Add a `hera_rebind` row to the README Reference MCP tools table.

## 6. Verification

**Depends on:** Stage 4

- [x] 6.1 `make pre-pr` passes clean (build/vet/fmt-check/lint-pr/test-cover-gate green; `vuln` fails only on pre-existing Go-stdlib CVEs unrelated to this change's files, the documented toolchain-only/continue-on-error exception).
- [x] 6.2 Confirm every acceptance criterion in `design.md` has a corresponding passing test.
- [x] 6.3 Archive this change (`openspec archive` or the manual merge-and-move fallback) in the same PR, before merge.
