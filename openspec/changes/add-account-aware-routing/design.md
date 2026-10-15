## Context

`[backend_routing]` with `strategy = "headroom"` (from `fix-backend-usage-routing`) now picks between *backends* — `claude` and `codex` — by `threshold_pct − weekly used %`. Aaron has two Claude logins (work, his main account, and personal) plus one Codex login, and wants Hera workers balanced across all three while coordinators stay on Claude.

Today routing cannot see accounts at all:

- `config.BackendTier` has only `backend` / `probe` / `threshold_pct`.
- The Claude `/usage` probe (`internal/usagebudget`) is deliberately default-account-only: it strips `CLAUDE_CONFIG_DIR` and probes `~/.claude`, into a single cache.
- Hera workers, sub-coordinators and freelancers inherit the spawning coordinator's account (`agent-execution`: "Account resolved once at task creation and immutable"). An inherited account that cannot run the resolved backend is rejected, so a coordinator on a Claude-only account cannot spawn a Codex worker.
- Coordinators bypass the tier list entirely (`resolveCoordinatorBackend`) and run on whatever account they inherit or default to.

The agent-accounts machinery (named accounts with their own `claude_config_dir`, symlinked shared entries, per-account resume/discovery/reaping) exists but has not been exercised live.

## Goals / Non-Goals

**Goals:**

- A Hera worker / freelance spawn (and ordinary task creation) with no explicit backend or account is routed to whichever of {Claude on work, Claude on personal, Codex} has the most headroom.
- A coordinator / sub-coordinator spawn stays on Claude, prefers work, and falls back to personal only when work is at or above a separate, higher coordinator limit.
- Routing stays correct when Aaron re-signs `~/.claude` into a different login (e.g. work is burned out, so he switches the machine to personal).
- Every routing decision is observable in `daemon.log`, and per-account readings are visible in the TUI status bar.

**Non-Goals:**

- Moving a running or resumed task to another account (task accounts stay immutable).
- Routing on 5-hour windows (weekly only, as in `fix-backend-usage-routing`).
- Multiple Codex logins (one Codex tier, default Codex home).
- Any account field on diligence profiles / archetypes. Profiles keep mapping archetype → model per backend family; a worker routed to personal Claude uses the same Claude model its archetype names.
- A Settings-screen surface for the new keys (`config.toml`-only, same precedent as `strategy`).
- Web and macOS display of per-account readings. Usage readings are not REST-exposed today (the status bar reads them over the daemon socket); exposing them over REST for the web SPA and macOS app is a named follow-up, not silently dropped.

## Decisions

### D1. A tier gains an optional `account`

```toml
[backend_routing]
strategy = "headroom"
coordinator_accounts = ["default", "personal"]
coordinator_threshold_pct = 95

[[backend_routing.tier]]          # work: account omitted = "default" = ~/.claude
backend = "claude"
probe = "claude_usage"
threshold_pct = 70

[[backend_routing.tier]]          # personal
backend = "claude"
account = "personal"
probe = "claude_usage"
threshold_pct = 90

[[backend_routing.tier]]          # codex
backend = "codex"
probe = "codex_usage"
threshold_pct = 80

[accounts.personal]
claude_config_dir = "~/.claude-personal"
```

An omitted `account` means `default`. A tier naming an unknown account, or an account that cannot run the tier's backend (`Config.AccountSupports`), is skipped and logged — never fatal, matching "A misconfigured tier is skipped, never fatal".

**Account layout (decided with Aaron):** work stays in `~/.claude` (the `default` account), because that is where his memory, history and global settings live. Personal becomes a named account in its own folder, which shares `CLAUDE.md`, skills, commands and agents by symlink but not per-project memory/history (`NeverInheritClaude` excludes `projects/`).

### D2. The resolver returns backend + account

`backendtier.ResolveBackend(cfg) string` becomes a resolver returning a `Resolution{Backend, Account}`. The headroom and ordered algorithms are unchanged; they now range over tiers whose identity is (backend, account-identity) instead of backend alone.

### D3. A routed account replaces coordinator inheritance

Account precedence at creation becomes: explicit account → routed account (when routing chose the backend) → inherited account (hera spawn / MCP caller) → project `project_accounts` → `default_account` → `default`.

Routing only supplies an account when it actually chose the backend — i.e. no explicit backend and no project backend. If the caller pins a backend or the project does, routing is not consulted and today's inheritance applies unchanged. When an explicit account is given without an explicit backend, routing considers only tiers on that account (so pinning `personal` can never land on a Codex tier the account cannot run); if none qualifies, the backend falls through to default-backend precedence.

This keeps the "explicit wins" precedent and fixes the Codex-worker-under-Claude-only-coordinator rejection for routed spawns, since a routed Codex worker gets the Codex tier's own account rather than the coordinator's.

### D4. Usage readings are keyed by signed-in identity, not account name

Before each probe tick, the daemon resolves each Claude tier's account folder to its signed-in email via the existing `claudeaccount.CachedAuthStatus` (reads the email only, never credentials). Then:

- Tiers whose folders report the same email collapse into one candidate with one reading. The collapsed candidate's account is the `default` account if it is among them (it carries full memory/history), else the first-listed tier's account.
- A Claude tier whose folder reports no sign-in is skipped (not fail-open unknown): spawning there would just hit a login prompt.
- If the sign-in lookup itself errors, the tier is treated as unknown (fail open), not skipped — a transient lookup failure must not remove a real account.

The `/usage` probe then runs once per distinct identity, inside that account's own `CLAUDE_CONFIG_DIR`, in the existing dedicated empty probe directory with the existing trust-dialog handling. Readings are cached per identity.

This is what handles "work is burned out, so I switch my machine to personal": `~/.claude` now reports the personal email, the work tier and the personal tier collapse, and routing balances personal against Codex. Signing `~/.claude` back into work restores three candidates with no config change. Identity is re-resolved every tick (and at daemon startup), so a switch is picked up within one probe interval.

### D5. Coordinator account selection is a separate ordered walk

Coordinators and sub-coordinators keep `resolveCoordinatorBackend` (always Claude-capable) and gain account selection: walk `coordinator_accounts` in order and take the first account whose reading is below `coordinator_threshold_pct`. An unknown reading counts as available. A not-signed-in account, or one that cannot run Claude, is skipped and logged. If every listed account is at or over the limit, take the first listed — a coordinator always spawns. An explicit account on the spawn wins. Collapsed identities (D4) apply: if `default` and `personal` are the same login, the walk lands on it once.

When `coordinator_accounts` is unset, coordinator account resolution is unchanged from today.

### D6. Observability

Every resolution logs one `slog` line to `daemon.log` (not `uxlog`, which is a no-op in the daemon): each candidate's identity, reading, threshold and headroom, the winner, and the reason (headroom / list-order / all-over-threshold / coordinator-fallback). The status bar renders one segment per distinct identity, labelled by account name (`work 100% · personal 36% · cdx 25%`), carried over the existing daemon `BootInfo` RPC as an additive list.

## Alternatives considered

- **A. Account on each tier (chosen).** Small, reuses the shipped headroom resolver, and leaves account-scoped resume/discovery/reaping on the existing agent-accounts machinery.
- **B. Model the second account as a separate backend** (`claude-personal` with `CLAUDE_CONFIG_DIR` in env). Rejected: resume, session discovery and stray-session reaping find sessions via the task's *account*, not backend env vars, so they would look in the wrong folder.
- **C. Swap logins inside one `~/.claude`.** Rejected: races running sessions and can't run both accounts concurrently.

## Discovery findings

- `~/.claude` is currently signed into personal (anutron@gmail.com); the live 36% reading is personal usage. No accounts are configured yet.
- `resolveTaskAccount` rejects an inherited account that cannot run the backend — so today a coordinator on a Claude-only account cannot spawn a Codex worker. Routed spawns must supply the account themselves.
- The base `agent-execution` account requirement contradicts itself: its text (and `resolveTaskAccount`) reject an inherited account that cannot run the backend, but its "Inherited account lacking the backend's tool" scenario says the worker is created on `default`. This change's MODIFIED copy corrects the scenario to match the text and code.
- The status bar labels the `default` identity `cla` (unchanged from today) and other identities by account name.
- `internal/claudeaccount.CachedAuthStatus(ctx, dir)` already returns `LoggedIn` and `Email` for a Claude config dir and is used by `GET /api/accounts`.
- The `fix-backend-usage-routing` change is still open (verification + archive pending). This change only ADDS requirements to `backend-tier-routing` rather than modifying the headroom requirement, so it does not depend on that change's archive order.

## Risks / Trade-offs

- **Workers on the personal folder lack per-project memory/history.** Accepted; collapse-prefers-`default` (D4) means this only happens when work and personal are genuinely different logins.
- **Per-identity probes cost one extra headless `claude /usage` run per account per tick.** Small; the probe spends no model tokens.
- **Untested agent-accounts code.** Mitigation: manual account setup and a live spawn on `personal` is the first verification step, before routing relies on it.
- **Identity collapse depends on the email lookup.** A lookup error fails open to "unknown", so worst case is an extra probe or a list-order pick, never a removed account.

## Migration Plan

1. Deploy the build.
2. Aaron signs `~/.claude` into work (when he wants work back in rotation) and runs `CLAUDE_CONFIG_DIR=~/.claude-personal claude` once to log the personal folder in.
3. Add `[accounts.personal]`, the three tiers, `coordinator_accounts` and `coordinator_threshold_pct` to `~/.argus/config.toml`.
4. Restart the daemon; confirm per-identity probe lines in `daemon.log` and three segments in the status bar.

Rollback: remove `account` / `coordinator_*` keys; tiers without accounts behave exactly as today.

## Open Questions

None.

## Acceptance criteria

**Worker routing**

- It should send a worker to whichever of work, personal or Codex has the most headroom below its threshold.
- It should give a routed Claude worker the tier's account, not the coordinator's.
- It should give a routed Codex worker the default Codex login even when the coordinator is on a Claude-only account.
- It should honor an explicit backend or account in the spawn call over routing.
- It should skip a tier naming an unknown account or an account that cannot run the tier's backend, and continue resolving.

**Identity and probes**

- It should treat two tiers whose folders are signed into the same email as one candidate with one reading.
- It should prefer the `default` account when collapsing duplicates.
- It should skip a Claude tier whose folder has no sign-in.
- It should probe each distinct Claude identity inside that account's own config folder.
- It should pick up a re-signed `~/.claude` within one probe interval with no config change.

**Coordinators**

- It should put a new coordinator on the first listed account under the coordinator limit.
- It should fall back to personal for a coordinator when work is at or over the coordinator limit.
- It should use the first listed account when all are over the limit, and never fail the spawn.

**Visibility**

- It should log each routing decision with per-candidate readings in `daemon.log`.
- It should render one status-bar segment per distinct identity, labelled by account name.
