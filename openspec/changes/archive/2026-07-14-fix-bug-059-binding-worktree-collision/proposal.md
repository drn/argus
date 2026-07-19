## Why

A born-bound worker (materialized from a plan-DAG node) could get permanently stuck: it could neither claim its hera binding nor attach a new one, and the two failures contradicted each other.

- `hera_join(cwd, orchestrator=X)` in claim mode reported **no binding exists**.
- `hera_join(cwd, orchestrator=X, role_name=…, kind=…)` in attach mode failed with **`UNIQUE constraint failed: hera_bindings.worktree_path, hera_bindings.orchestrator_id`** — a binding already exists.

Both statements were about the same `(worktree_path, orchestrator_id)` pair. The root cause: identity is resolved through two different keys that disagree.

- Claim/attach lookups key on `argus_task_id`, resolved from `cwd` via the cwd→task resolver (`resolveTask`).
- The `hera_bindings` INSERT's uniqueness is enforced on `(worktree_path, orchestrator_id)` (`idx_hera_bindings_live_worktree_orch`).

`worktree_path` is not a stable unique key across a task's full lifecycle: argus reuses a worktree directory when a task name/branch is reused after the prior task moved to `in_review`/`complete`/archived without its worktree being cleared. Live evidence (`~/.argus/data.sql`, read-only): two argus tasks shared `/Users/aaron/.argus/worktrees/Sketch/5a-verify` — a stale, archived `in_review` task (orchestrator `restore-fork-variants`, binding already ended) and the live `in_progress` worker (orchestrator `sketch-blueprint-comments-apply`, binding live). `resolveTask` returned the first-listed match, so a `cwd` could resolve to the **stale** task id: the task-keyed claim then missed the live binding that the worktree-keyed uniqueness nonetheless rejected on attach. The same first-match hazard is why `argus_clipboard_set(cwd=…)` resolved to a stale 17-day-old task elsewhere — `resolveTask` is shared across every cwd-addressed MCP tool, not just hera's.

This is the third recurrence of the plan-node/binding race family (early materialization, fixed via transactional `hera_plan`; the one-directional binding-lookup failure fixed by `hera_move`; now the shared-worktree collision), so the fix matches that precedent's rigor: a deterministic invariant plus regression tests, not a point patch.

A standalone reference implementation of this same fix already landed on the separate `anutron/hera` repo as PR #156; this change ports that fix into argus's native in-tree hera implementation (the repo the deployed daemon actually runs), adapted to argus's DB/MCP structure.

## What Changes

- **`resolveTask` (the shared cwd→task resolver used by every cwd-addressed MCP tool) disambiguates a shared worktree instead of returning the first match.** Among tasks tied for the longest matching worktree path (which, since prefixes of one string at a fixed length are identical, means they share the exact same `Worktree` value): one match → unchanged; else drop archived matches; one non-archived match left → return it; else prefer the single `in_progress` match (the running caller); else refuse with an error naming the candidate tasks rather than guess.
- **Binding identity lookups fall back from task-keyed to worktree-keyed.** A new `HeraLiveBindingByWorktreeAndOrchestrator(worktreePath, orchID)` DAO method is the orchestrator-scoped twin of `HeraLiveBindingByTaskAndOrchestrator`. `resolveCallerRole` (used by `hera_send`/`hera_status`/`hera_inbox`/`hera_spawn_worker`/etc.) tries the resolved `argus_task_id` first and, on a miss, falls back to the caller's `worktree_path` — the same key the live-uniqueness index is defined on, so a claim now succeeds precisely when an attach would have collided. Orchestrator scoping keeps the fallback safe: a stale binding under a *different* orchestrator sharing the worktree is never returned.
- **`hera_join` attach mode and `hera_new_orchestrator` bootstrap pre-check the worktree-keyed binding** and return an actionable message ("this worktree already holds a live binding … claim it via hera_join, or hera_rebind if the binding's task id has drifted") instead of leaking a raw `UNIQUE constraint failed` error.
- **New `hera_rebind` MCP tool** — the supported repair path for the harder case where the live binding's own `argus_task_id` has drifted (so the task-then-worktree fallback above still cannot make the two keys agree, because the binding row itself needs to change). It reconciles the binding to the caller's real live task WITHOUT tearing down the argus session: the role (and thus its prompt, messages, and status, all keyed on `role_id`) is preserved; only the binding row is refreshed (end the stale one, insert a clean one under the same role). It refuses rather than guesses when genuinely ambiguous (two live `in_progress` tasks share the worktree; multiple roles are bound here with no `role_name` to pick; a different role already holds the target slot; nothing to reconcile).
- A claim (`hera_join` with no `role_name`) never rewrites a binding's `argus_task_id` — it only resolves and returns the existing binding's identity. Repair is the separate `hera_rebind` act.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-coordination`: the "Caller role resolution from cwd" requirement gains the task-then-worktree fallback; "hera_join claims an existing role or attaches a new one" and "hera_new_orchestrator bootstraps and claims the coordinator role" gain the worktree-keyed collision guard; "Native hera_* MCP tool surface" tool count/list is updated to include `hera_rebind`. A new requirement captures cwd resolution's shared-worktree disambiguation, and a new requirement specs `hera_rebind` itself.

## Impact

- **Code:** `internal/db/hera.go` (new `HeraLiveBindingByWorktreeAndOrchestrator`), `internal/mcp/server.go` (`resolveTask` disambiguation + new `CwdAmbiguousError`), `internal/mcp/hera.go` (`resolveCallerRole` fallback via new `liveBindingForOrch`/`liveBindingForTask` helpers; `toolHeraJoin` attach-mode + `toolHeraNewOrchestrator` worktree collision guards; new `toolHeraRebind` handler + tool registration), `internal/mcp/server.go` tool dispatch switch.
- **Tests:** `internal/db/hera_bindings_worktree_test.go` (worktree-keyed lookup + claim-vs-attach DAO-level reproduction), `internal/mcp/resolve_cwd_test.go` (`resolveTask` disambiguation), `internal/mcp/hera_rebind_test.go` (`hera_join` claim/attach agreement across a stale-worktree collision + full `hera_rebind` coverage), updated tool-count assertion in `internal/mcp/hera_test.go`.
- **Docs:** README Reference MCP tools table gets a `hera_rebind` row.
- **Data:** none — no schema migration (`hera_bindings.worktree_path`/`orchestrator_id` and their unique index already exist). No live-DB repair is part of this change; the daemon's own fixed code path (a restart, or `hera_rebind` once deployed) is how any already-stuck live binding gets reconciled.
- **No REST/TUI/macOS surface changes** — the affected tools are MCP-only; nothing in the three frontends calls them directly.
