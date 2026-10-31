## Context

`protect-claude-quota` routes new tasks across (backend, account) tiers using both Claude windows, from per-account background probes plus bridge readings (`usagebudget`). A running task keeps its stored account for life. `runner.KickRerender` already restarts a task in place with `--resume` and carries `KickReq.Account` to the supervisor. An earlier draft of this change (`add-account-auto-routing`, never merged) added a separate `auto` account and usage ledger; it was dropped in favour of tier routing, and only its move and worker-routing parts survive here. It was dogfooded live before being cut down.

## Decisions

### D1. Move = copy, persist, kick

`agent.MoveTaskAccount`:

1. Claim a per-task in-flight guard.
2. Load the task. Require a Claude backend, a known target that supports it, and a target different from the current account.
3. Refuse when a restart is already pending, or when the session is live and not idle unless `force` is set.
4. Refresh the resume session ID.
5. Copy `<id>.jsonl` and its `<id>/` sidecar from the source account's `projects/<encoded worktree>/` into the target's, via temp file and rename, checking both the logical and symlink-resolved worktree paths. Never delete the source.
6. Persist with a targeted `SetTaskAccount(id, account, routed=false)`.
7. Then `KickRerender` with the session's own PTY size, or `Revive` when there is no transcript.

Persisting before the kick matters: `pendingRestart` suppresses the exit handler's status flip and the hera roll-to-review. If the kick fails while the old session is still running, restore the previous account and routed flag and report failure. If the old session died, return `Started=false` and let the caller restart it.

### D2. Usage source

`usagebudget.CachedReadingForAccount(account)` returns the account's bridge reading (else probe cache) with both windows. It is the only usage source for the picker, the `auto` target and `GET /api/accounts`. The local TUI reads it through a new daemon RPC `AccountUsage` (error → every account unknown, logged under `[account]`), because the probe caches live in the daemon.

### D3. `auto` move target

Over `cfg.AccountNamesFor("claude")` minus the current account, pick the lowest binding percentage (the fuller valid window). Unknown counts as 0%; ties go to candidate order (`default`, then names sorted). No candidate → reject.

### D4. Routed flag and inheritance

`CreateAndStart` sets `tasks.account_routed` when the stored account came from the routed tier choice (no explicit or project account). `agent.InheritAccount(parent)` returns `""` for a routed parent (so the child routes) and `PinnedAccount(parent.Account)` otherwise. It is used by hera worker spawn (`resolveOrchestratorAccount`) and MCP `task_create`. A routed child that lands on an account is flagged routed in turn. Forks keep the source account (unchanged).

### D5. Safety rails

- A forced move of a busy session copies the transcript before the kick stops the agent, so the last in-flight turn may be lost. This is accepted: the rescue case is an idle, capped agent.
- A self-move through MCP is rejected (the restart would kill the session awaiting the reply).
- The Hera rail `>` is inert in remote mode.

## Risks / Trade-offs

- Folder trust on the target account: not observed in dogfood.
- An account that is neither a tier nor running agents has no probe, so it shows "—" in the picker and counts as 0% for `auto`.
