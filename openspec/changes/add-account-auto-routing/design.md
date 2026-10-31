## Context

- Account resolution lives in `config.ResolveAccount` + `agent.resolveTaskAccount`, called once from `CreateAndStart`. Hera spawns pass the coordinator's stored account as an explicit, inherited pick (`resolveOrchestratorAccount` → `PinnedAccount`), so a default-account coordinator pins all its workers to `default`.
- Each live Claude session already reports `rate_limits` (`five_hour`, `seven_day`, each `{percent_used, resets_at}`) through the bridge (`session.measure`). `Server.BridgeUsage(account)` reads only `seven_day`, only from live hub states measured within the last hour; hub states are pruned when sessions end, so a cap is forgotten the moment its sessions go away.
- Task creation runs in two processes: the daemon (REST, MCP, hera spawns, headless) and the local TUI (`n` form, fork). The TUI already reaches daemon-only state over the TUI socket (`Client.BridgeStates`, `BootInfo`).
- Claude transcripts live at `<config_dir>/projects/<EncodeProjectDir(worktree)>/<session-id>.jsonl` (plus an optional `<session-id>/` sidecar dir for subagent/tool-result files). `projects/` is never shared between accounts, so resuming on another account needs the transcript copied.

## Goals / Non-Goals

**Goals:**

- New tasks land on the account with budget, judged on both the 5-hour and weekly windows.
- A stuck task can be moved to another account and resume the same conversation, by the user (TUI) or by a coordinator acting on the user's request (MCP).
- Deliberate account choices (explicit picks, project pins) are never overridden.

**Non-Goals:**

- Automatic moves of capped sessions (user decision: manual only).
- A "stuck on cap" indicator.
- Probing accounts that have no live session (readings come only from live sessions; unknown = available).
- Codex transcript moves, the web/macOS move action, a Settings picker for `auto` (named follow-ups).

## Decisions

### D1. `auto` is a reserved account value resolved at creation, recorded with a flag

`auto` joins `default` as a reserved name (cannot be defined as an `[accounts.*]` table). `ResolveAccount` may now return `auto`; `resolveTaskAccount` replaces it with the concrete pick before anything is persisted, and sets `task.AccountAuto = true`. The stored `account` stays a concrete name, so spawn, resume, discovery, and the reaper are untouched. The flag lives in a new `tasks.account_auto INTEGER NOT NULL DEFAULT 0` column, not `task_meta`, because `SetArchived` wipes task_meta and the flag must survive for inheritance.

*Alternative:* store `account = "auto"` and resolve on every spawn. Rejected: a resumed conversation must stay on the account that holds its transcript.

### D2. Selection rule

Candidates are `cfg.AccountNamesFor(backendCommand)` (`default` first, then names sorted). For each candidate and each window W ∈ {five_hour, weekly}: `headroom_W = threshold_W − pct_W`, where a missing/expired reading counts as `pct_W = 0`. `score = min(headroom_5h, headroom_weekly)`. Pick the highest score; ties go to candidate order. When every score is ≤ 0 the highest score still wins (least over) and the outcome is logged. Thresholds come from `[account_routing]` (`five_hour_threshold_pct`, `weekly_threshold_pct`, both default 90, values outside 1..100 ignored with a warning).

Unknown-as-available matches the existing fail-open routing and means an idle account gets the next task, which produces a reading after its first turn. Readings exist only for Claude, so for a Codex backend every candidate is unknown and the pick is the first candidate (`default`), the same as today.

### D3. Usage ledger (`internal/accountusage`)

A small concurrency-safe store: `Record(account, window, pct, resetsAt, measuredAt)` and `Snapshot() map[account]Usage`. Per (account, window) it keeps the newest measurement; a reading is dropped once `resetsAt` has passed. It is fed from the API server's bridge-measure path (the same place `stampBridgeMeasurement` runs), with the task's stored account (`""` normalized to `default`). It is in memory only. A daemon restart forgets readings, which is safe because unknown means available. Within a window usage only rises, so an older reading is a lower bound and keeping it until reset never overstates headroom by more than what was used since.

`Server.BridgeUsage` (the backend-tier `claude_usage` source) is left as is. The ledger is a separate reader used only for account choice.

### D4. One selection function, two processes

`agent` gets a package-level `AccountUsageSource func() map[string]accountusage.Usage`. The daemon sets it to the ledger's `Snapshot`; the local TUI sets it to a new TUI-socket RPC `AccountUsage` on the daemon client (error → empty map → all unknown). Remote TUI creates and moves through REST, so the work happens daemon-side. `accountusage.Pick(cfg, candidates, snapshot)` is pure and unit-tested.

### D5. Backend and account interplay

`resolveDefaultBackendName` runs before account resolution and feeds `backendtier.ResolveBackend(cfg, account)`. When the account would resolve to `auto`, the backend tier is fed the account `Pick` would choose for a Claude backend, so the `claude_usage` tier reads the account that would actually run the task. Then `Pick` runs again over the candidates that support the chosen backend.

### D6. Inheritance

`resolveOrchestratorAccount` returns `auto` (instead of `PinnedAccount(t.Account)`) when the coordinator task has `AccountAuto`. MCP `task_create` caller inheritance does the same. An explicit `auto` re-runs selection, and the new task is flagged auto too, so the whole subtree keeps routing. A coordinator on a deliberately chosen account keeps today's verbatim pin. Fork keeps the source's account and flag (the transcript is on that account).

### D7. Account move (`agent.PrepareAccountMove`)

1. Load the task. Reject: unknown task; a non-Claude backend; target `auto` with no other candidate; target equal to the current account; a target that does not support the backend; a session that is live and not idle unless `force` (`409` / tool error). An agent stuck on its cap normally sits idle at the prompt, but Claude Code may keep redrawing a rate-limit or retry screen, so the TUI always offers the move: on a busy session it warns that the agent will be interrupted and passes `force`. Nothing in the move runs a model turn, so it works when the source account is at 100%.
2. Resolve `auto` with `Pick` over the candidates that support the backend, excluding the current account.
3. If a session is live, stop it (`runner.Stop`) and wait up to 10s for it to exit.
4. Bootstrap the target config dir (`BootstrapClaudeConfigDir`), then copy `<src>/projects/<enc>/<sid>.jsonl` and the `<sid>/` sidecar dir if present into `<dst>/projects/<enc>/`, overwriting an older copy from an earlier move. The source stays in place, so moving back works and nothing is lost. The session ID comes from `RefreshResumeSessionID` first, so the newest in-place transcript is the one moved. No transcript means the move still proceeds: the session ID is cleared and the result says `resumed: false`.
5. Persist `account` (`""` for default) and `account_auto = false`. A move is a deliberate choice, so the task and its future workers stay on the target.
6. Return `{from, to, resumed}`. Each caller restarts through its own existing path: REST/MCP reuse the `handleRestartTask` body (`StartOrReattach(..., resume=true)` + `in_progress`), and the TUI uses `startSession` so pane wiring happens. No new task is created: the task row, worktree, branch, and hera bindings are untouched (same task ID). Only the agent process is replaced by one running under the target account with `--resume`, so the hera role and its message delivery carry over unchanged.

MCP `task_move_account` rejects a caller moving its own task (the call would kill the session waiting for the response) with a message pointing to the user or coordinator. It accepts `task_id`, `account`, and `caller_id`.

### D8. TUI

`>` (`tasklist.move_account`, `hera.move_account`, both rebindable) opens a picker modal listing `auto` plus each candidate account other than the current one, labelled with its 5-hour and weekly % from `AccountUsageSource` ("—" when unknown). Enter confirms; the move runs off the UI thread and reports via the status-bar notice. A busy session still gets the picker, with an interrupt warning, and the move is sent with force. Remote mode calls `POST /api/tasks/{id}/account`. The new-task form's account selector lists `auto` first when the backend has at least one named candidate, preselected when the project/global default resolves to `auto`.

## Risks / Trade-offs

- [A move copies a work-account conversation into the personal account dir, or the reverse] → It is an explicit user or coordinator action, never automatic; the README Reference row says so.
- [Readings exist only while some session on the account is live] → Unknown counts as available, and a capped account is remembered until its reset even after its sessions end. Per-account probing is a follow-up.
- [Ledger lost on daemon restart] → Fail-open. The next measure from any live session repopulates it within one turn.
- [The resumed session on the new account may hit a folder-trust prompt for the worktree] → Same path as any new task on that account; verify live during dogfood (task 8.2).
- [Old daemon + new TUI] → The `AccountUsage` RPC errors, the TUI treats every account as unknown, and auto picks the first candidate. Logged under `[account]`.

## Migration Plan

Additive column migration (`account_auto`, default 0) through the existing schema-migration path. With no `auto` anywhere in config, behavior is unchanged. Rollback: remove `auto` from config.
