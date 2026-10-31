## Context

Routing currently scores only Claude weekly usage and returns a backend name. Account selection happens separately. Existing bridge state is scoped by task account; the headless probe has one default-account cache. All creation paths converge on CreateAndStart, while coordinators additionally enforce Claude capability.

## Goals / Non-goals

**Goals:** Protect either Claude quota window, select backend/account together, and use idle Claude accounts even when Claude is pinned.

**Non-goals:** See the named frontend parity follow-up in the proposal. Preserve explicit/inherited nonblank account pins; blank parent accounts permit routing.

## Decisions

- Retain both Claude windows, selecting the fullest still-valid window at read time, so a session reset immediately reveals a still-valid weekly reading. Bridge selection first picks the freshest valid measurement per window, then compares utilization. Keep the existing one-hour freshness policy.
- Return a routing choice containing backend and account; preserve backend-only wrappers where existing callers need them. Tier account omission uses the existing resolved account. Explicit/project account pins override tier accounts during scoring and stamping.
- Validate configured tier accounts with ValidateAccount and AccountSupports; skip invalid tiers with both log channels. Filter explicit-backend choices to that backend, and coordinator choices to Claude-capable backends. Retain existing ordered/headroom strategies, ties, and fail-open fallback.
- Resolve the pair once before creation side effects and persist it. Resume uses the stored values without rerouting. Emit both logs with backend, account, source/reason at creation.
- Persist optional account in backend_tiers with the repo's established schema-update mechanism, and cycle compatible named accounts in Settings. Preserve config.toml ownership/read-only behavior.
- Probe default and deduplicated configured Claude tier accounts at startup and every 30 minutes, caching independently. Each fresh account-scoped bridge reading skips that account's hidden probe. Set CLAUDE_CONFIG_DIR and strip the same inherited auth overrides as task spawning.
- Relay the binding window through BootInfo and render `s` for session or `w` for week in the status bar. A time-only session reset may roll to the next day only within five hours; explicitly dated expired resets remain ignored. Wait for weekly usage to paint before ending a hidden probe.

## Risks / Trade-offs

- Failed named probes fail open → preserve existing unknown-tier strategy and log the account and failure.
- Textual session reset formats vary → test real fixture layouts and session-only reset times, reject invalid/expired windows independently.
- Nonblank Hera inherited accounts override routing → retain deliberate account isolation while allowing blank-account parents to route.

## Migration plan

Optional account defaults empty for existing tiers. Archive in the same PR. Operators add a personal Claude tier to opt into overflow; rollback removes that tier and reverts the PR.

## Open questions

Coordinator approved with named-account hidden probing required and blank parent accounts unpinned.

## Observed spawn evidence

On 2026-10-30, five recent explicit-Claude worker calls in a readable recruiting coordinator transcript had no mission reference to self-promotion, a Claude-only connector, or a Claude review panel. This confirms unnecessary pins occur, but does not establish the coordinator’s internal reason. Shipped promotion/help wording prominently suggested explicit Claude; ordinary worker guidance now explicitly says to omit backend.
