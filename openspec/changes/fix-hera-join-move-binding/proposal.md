## Why

`hera_join`'s attach mode silently creates an extra live binding under a new orchestrator even when the calling task already holds a live binding elsewhere — there's no relationship check, no warning, and freelance bindings never expire. This produced a real, confusing case (task `11a-archive`, a proper worker under `coordctx-exec`, also permanently bound as a `freelance` role under an unrelated older orchestrator) and violates the expected mental model that joining a coordinator moves membership rather than duplicating it.

## What Changes

- `hera_join` attach mode now **ends the caller's existing live binding under any other orchestrator** (setting `ended_at`/`end_reason: "moved"`, transactionally with the new binding's creation) before creating the new role+binding — move-by-default. **BREAKING** for any caller that relied on the prior silent-duplicate behavior.
- `hera_join` gains a new optional boolean parameter, `keep_existing`, that skips the move and preserves today's duplicate-binding behavior for a deliberate multi-home case.
- The attach-mode response reports which prior binding (orchestrator + role name), if any, was ended.
- Existing same-orchestrator conflict rejection (attaching to an orchestrator the caller is already live-bound to) is unchanged.
- `hera_new_orchestrator`'s multi-binding allowance for worker self-promotion / `subcoord` is unchanged — it's a separate code path not touched by this change.
- One-off data cleanup: end the specific stray binding found during investigation (`hera_bindings.id=596`, role `11a-archive-report`, kind `freelance`, orchestrator `hera-model-tasks`) via a targeted one-off script against the live DB — not part of the shipped code path.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-coordination`: the "hera_join claims an existing role or attaches a new one" requirement changes to describe move-by-default + the `keep_existing` override; the "Task may bind under multiple orchestrators" scenario is clarified to state that plan `hera_join` no longer produces cross-orchestrator multi-binding by default — that now only happens via `hera_new_orchestrator` self-promotion or an explicit `keep_existing: true`.

## Impact

- **Code:** `internal/mcp/hera.go` (`toolHeraJoin` attach-mode branch), `internal/db/hera.go` (binding-end helper, reused/extended if needed).
- **Tests:** `internal/mcp/hera_test.go` (or equivalent) for the new move-by-default, `keep_existing` override, and unchanged same-orchestrator-conflict / self-promotion behavior.
- **Docs:** `context/knowledge/gotchas/orchestration.md` (hera schema/store bullet) gets a short addition noting the move-by-default behavior, since it documents the multi-binding model.
- **Data:** one-off live-DB fix for the single identified stray binding; no schema migration.
- **No REST/TUI/macOS surface changes** — `hera_join` is MCP-tool-only; nothing in the three frontends calls it directly.
