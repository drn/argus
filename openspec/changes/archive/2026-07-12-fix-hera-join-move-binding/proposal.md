## Why

`hera_join`'s attach mode silently creates an extra live binding under a new orchestrator even when the calling task already holds a live binding elsewhere — there's no relationship check, no warning, and freelance bindings never expire. This produced a real, confusing case (task `11a-archive`, a proper worker under `coordctx-exec`, also permanently bound as a `freelance` role under an unrelated older orchestrator) and violates the expected mental model that relocating to a different coordinator should be a deliberate act, not an implicit side effect of joining.

## What Changes

- `hera_join` attach mode now **rejects** the call — instead of silently creating a second binding — when the calling task already holds a live binding under a *different* orchestrator than the one being joined, and directs the caller to the new `hera_move` tool. **BREAKING** for any caller that relied on the prior silent-duplicate behavior.
- **New MCP tool `hera_move`**: required args `cwd`, `orchestrator` (target), `role_name`, `kind` (`worker`|`freelance`; `coordinator` rejected, mirroring `hera_join`); optional `from_orchestrator` (required only to disambiguate when the caller holds 2+ live bindings) and optional initial `status`. Ends the caller's resolved current live binding (`ended_at`/`end_reason: "moved"`) and creates the new role+binding under the target orchestrator, transactionally. Rejects with a "nothing to move" error when the caller holds no live binding, and with a no-op error when the target equals the resolved source orchestrator.
- The `hera_move` response reports the source orchestrator + role name that was moved, plus the new binding id.
- Existing `hera_join` same-orchestrator conflict rejection (attaching to an orchestrator the caller is already live-bound to) is unchanged, as is the unbound-caller attach path.
- `hera_new_orchestrator`'s multi-binding allowance for worker self-promotion / `subcoord` is unchanged — it's a separate code path not touched by this change, and remains the only sanctioned way a task can hold 2+ live bindings. No opt-out/multi-home escape hatch is added to `hera_join` or `hera_move` — none was found to be needed.
- One-off data cleanup: dropped 2026-07-12. Ending the specific stray binding found during investigation (`hera_bindings.id=596`, role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`) would require either an agent bypassing its sandbox (not acceptable) or Aaron hand-running raw SQL against the live DB — not worth it for one cosmetic row with no functional impact. Aaron will manage/ignore it via the TUI instead.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-coordination`: the "hera_join claims an existing role or attaches a new one" requirement changes to add the cross-orchestrator rejection case; a new "hera_move relocates the caller's binding to a different orchestrator" requirement is added; the "Native hera_* MCP tool surface" requirement's tool count/list is updated to include `hera_move`; the "Task may bind under multiple orchestrators" scenario is clarified to state that `hera_join`/`hera_move` no longer produce cross-orchestrator multi-binding — that now only happens via `hera_new_orchestrator` self-promotion.

## Impact

- **Code:** `internal/mcp/hera.go` (`toolHeraJoin` attach-mode branch gains a rejection case; new `toolHeraMove` handler + tool registration), `internal/db/hera.go` (new move-capable role+binding creation, transactional).
- **Tests:** `internal/mcp/hera_test.go` and `internal/db/hera_test.go` for the new `hera_join` rejection, the new `hera_move` tool's happy path + error cases (nothing to move, same-orchestrator no-op, disambiguation, coordinator-kind rejection), and unchanged same-orchestrator-conflict / self-promotion behavior.
- **Docs:** `context/knowledge/gotchas/orchestration.md` (hera schema/store bullet) and the README Reference MCP tools table get short additions for `hera_move` and the `hera_join` rejection.
- **Data:** none — the one-off live-DB fix for the stray binding was dropped; no schema migration.
- **No REST/TUI/macOS surface changes** — `hera_join`/`hera_move` are MCP-tool-only; nothing in the three frontends calls them directly.
