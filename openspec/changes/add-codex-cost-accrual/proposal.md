## Why

Hera cost estimates only measure Claude sessions: accrual runs off `argus coord-hook` (a Claude Code Stop hook) and the Claude Code bridge mod, neither of which ever fires inside a Codex session. Every Codex-backend role therefore accrues $0, so any orchestrator that routes implementation to Codex badly understates its spend. Example: `sketch-reporting` showed $262 after ~8 hours, but its 11 Codex workers (and their ~40 Codex sub-agent threads) made 4,447 API calls that price to roughly $167 more at OpenAI's published `gpt-6.1-sol` rates, so the true figure was about $430. The figure is meant as a published-API-price yardstick for "which of my work is expensive," not an actual bill, so it has to cover every backend that does real work.

## What Changes

- A daemon-side sweeper reads each live Codex-backend hera binding's Codex rollout files, found through Codex's own thread index by the binding's worktree `cwd`. This covers the main thread, Codex sub-agent threads, and resumed threads.
- Each Codex API call (one `token_count` event in a rollout) is priced individually at the moment it is first observed, using the model Codex itself recorded for that turn, and added to the binding's existing `cost_usd_accrued` and raw token columns. TUI, web, and macOS all read cost from those columns already, so no client changes are needed.
- On the first sweep after daemon start, a one-time backfill prices never-read Codex bindings that ended within the last 30 days, from the rollouts still on disk. This brings existing orchestrators like sketch-reporting up to their real totals; it is a Codex-only exception to the "no retroactive cost" rule. Bindings whose task row was deleted (nuked workers) fall back to the default Codex home.
- A per-binding, per-rollout read cursor makes accrual incremental and idempotent. A call is attributed only to the binding whose `[started_at, ended_at)` window contains its timestamp, and to none when the task was bound under two orchestrators at that moment, so neither re-binding nor dual-binding ever double-counts.
- The rate table gains an optional long-context tier per model: a threshold plus a second set of rates applied to any single call whose input exceeds it. OpenAI bills `gpt-6.1-sol` calls above 272K input tokens at a higher rate.
- `rates.toml` is seeded with `gpt-6.1-sol` (published standard and long-context rates), replacing the comment that says Codex models are deliberately unseeded.
- The backend family registry gains a cost-source capability so the sweeper selects Codex bindings without branching on backend identity.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `cost-estimation`: adds Codex rollout accrual (per-call pricing, cursor-based incremental reads, binding-window attribution, sub-agent inclusion, one-time 30-day backfill); the rate table requirement gains provider-reported model keys and an optional long-context tier; "No retroactive cost" gains the Codex backfill exception.

## Impact

- **Code:** new `internal/codexcost` package (rollout parser and per-call pricer); `internal/pricing` (long-context tier on `Rate`, per-call pricing helper); `internal/db` (new `hera_codex_cost_cursor` table, accrual transaction, live-Codex-binding query); `internal/daemon` (sweeper goroutine and startup wiring); `internal/agent/backends.go` (family cost-source capability); `internal/pricing/rates.toml` (new seed entry).
- **Data:** one new SQLite table, no changes to existing columns, no migration of existing rows. An already-installed `~/.argus/rates.toml` is never overwritten (existing rule), so an existing install needs a one-time manual edit or deletion to pick up the `gpt-6.1-sol` entry. This is called out in the migration plan.
- **Reads:** host-side files under the task's Codex home (`state_5.sqlite`, `sessions/**/rollout-*.jsonl`), read-only, by the daemon. This is the same files the existing session-ID capture and usage-routing probe already read.
- **Frontends:** no REST shape change; `cost_usd` and the raw token fields on `GET /api/hera` simply become nonzero for Codex roles. TUI, web, and macOS stay at parity with no client changes.
