## Why

Headroom routing balances only between backends (`claude`, `codex`), and the Claude usage probe reads only `~/.claude`, so Aaron's second Claude login is invisible to routing: his work account filled up while personal and Codex sat underused. He wants Hera workers balanced across work Claude, personal Claude and Codex, with coordinators kept on Claude (work first, personal as fallback), and routing that stays correct when he re-signs `~/.claude` into another login.

## What Changes

- `[[backend_routing.tier]]` gains an optional `account`; the resolver returns a backend **and** an account. Tiers naming an unknown account, or one that cannot run the tier's backend, are skipped.
- A routed account replaces coordinator/caller account inheritance at task creation. Explicit backend/account and project backend still win; when routing is not consulted, inheritance is unchanged.
- Usage readings are keyed by signed-in identity (email): tiers signed into the same login collapse into one candidate (preferring the `default` account); a Claude tier whose folder is not signed in is skipped; identity is re-resolved every probe tick.
- The Claude `/usage` probe runs once per distinct identity, inside that account's own `CLAUDE_CONFIG_DIR`.
- New `[backend_routing]` keys `coordinator_accounts` (ordered) and `coordinator_threshold_pct`: coordinators and sub-coordinators pick the first listed account under the limit, and always spawn.
- Each routing decision logs per-candidate readings to `daemon.log`; the status bar shows one segment per distinct identity, labelled by account name.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `backend-tier-routing`: tiers may name an account; resolution returns backend + account; candidates are keyed by signed-in identity; decisions are logged.
- `usage-budget-routing`: the Claude probe runs per distinct account identity inside that account's config folder.
- `agent-execution`: account precedence at creation inserts the routed account above inheritance.
- `hera-coordination`: coordinator / sub-coordinator spawn selects its Claude account from an ordered list against a coordinator limit.
- `tui-shell`: the status-bar usage readout renders one segment per distinct identity.

## Impact

- Code: `internal/config` (`BackendTier.Account`, `BackendRoutingConfig.CoordinatorAccounts` / `CoordinatorThresholdPct`), `internal/backendtier` (resolver returns backend + account, identity collapse, decision logging), `internal/usagebudget` (per-identity probe + cache), `internal/agent` (`create.go` account precedence, `hera_spawn.go` coordinator account selection), `internal/daemon` (probe loop per identity, `BootInfo` per-identity readings — additive TUI↔daemon RPC field), `internal/tui` status bar.
- Reuses `internal/claudeaccount.CachedAuthStatus` for identity lookup (email only, never credentials).
- No supervisor wire change: account is already carried on `StartReq` (ProtocolVersion 8+).
- No REST change. Web and macOS display of per-account readings is a named follow-up: usage readings are not REST-exposed today.
- `config.toml`-only; no Settings surface for the new keys (follow-up, same precedent as `strategy`).
