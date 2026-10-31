## Context

Cost accrual today (archived change `add-coordinator-cost-estimate`, plus the bridge-cost follow-up) has two sources, both Claude-only:

- **Transcript:** `argus coord-hook`, a Claude Code Stop hook, re-sums the session transcript and PUTs full raw totals. The daemon prices the delta at the current rate table and adds it to `hera_bindings.cost_usd_accrued`.
- **Bridge:** the Claude Code mod reports engine-computed cumulative USD, accrued against a persisted baseline in `hera_bridge_cost`.

Codex sessions trigger neither, so a Codex role's binding stays at all-zero and renders "n/a". Its parent coordinator's subtree total silently omits it.

Codex already persists everything needed, under the task's Codex home (default `~/.codex`, or the account's `codex_home`):

- **Thread index:** `state_5.sqlite` table `threads(id, rollout_path, cwd, source, model, created_at, updated_at, …)`. `cwd` is the worktree the thread ran in. Codex sub-agent threads appear in this table with the same `cwd` (verified: 12 of 14 threads for two sketch-reporting worktrees were `subagent` source). Argus already queries this table in `agent.CaptureCodexSessionIDIn`.
- **Rollout files:** `sessions/YYYY/MM/DD/rollout-*.jsonl`, append-only JSONL. A `turn_context` event carries `model`. A `token_count` event carries `info.last_token_usage` (that call's usage) and `info.total_token_usage` (running total) with fields `input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`. Verified on 51 real rollouts: summing `last_token_usage` across de-duplicated events reproduces the final `total_token_usage` exactly, `cached_input_tokens ⊂ input_tokens`, and `reasoning_output_tokens ⊂ output_tokens` (`total_tokens == input + output`).

Published pricing for `gpt-6.1-sol` (OpenAI, Oct 2026), per million tokens:

- **Standard:** $2.00 input, $0.10 cached input, $2.50 cache write, $10.00 output.
- **Calls above 272K input tokens:** $4.00 input, $0.20 cached input, $5.00 cache write, $15.00 output.

The long-context figures come from aggregator sites citing OpenAI, and the seed comment says so. In the sketch-reporting sample, 999 of 4,447 calls (22%) exceeded 272K input.

## Goals / Non-Goals

**Goals:**

- Codex-backend hera roles accrue cost into the same `cost_usd_accrued` and raw token columns Claude roles use, so every existing display and rollup includes them unchanged.
- Include Codex sub-agent threads spawned inside a worker's worktree.
- Price each call at published rates, including the long-context tier, keeping the existing invariant that already-accrued cost is immutable.
- Incremental, idempotent, crash-safe: a daemon bounce, a duplicate sweep, or a rebinding never double-counts.

**Non-Goals:**

- Pi, opencode, Antigravity, or custom backends. They still accrue nothing and show "n/a", as before. This is a named follow-up: each would need its own cost source on the family capability this change introduces.
- A "partial" marker on subtree rollups that include unmeasured roles (e.g. "$262+"). This would be a three-client UI change, deferred as a named follow-up.
- Correcting existing Claude rates in `rates.toml` (see Open Questions).
- Long-context pricing for Claude models. Claude's transcript path prices aggregate deltas, not individual calls, so the tier field is honored only by per-call pricers (Codex) in this change.
- Non-hera Codex tasks. Cost columns live on `hera_bindings`, and there is nowhere to accrue a plain task's cost today.

## Decisions

### D1. Discover rollouts through Codex's thread index, keyed on worktree `cwd`

The sweeper resolves the binding's task Codex home (`agent.CodexHomeForTask`, falling back to the same default `CaptureCodexSessionIDIn` uses). It then runs `SELECT id, rollout_path FROM threads WHERE cwd = ?` against `state_5.sqlite`, opened read-only.

- **Why `cwd`, not `tasks.session_id`:** in-progress Codex tasks have an empty `session_id` until post-exit capture, and a single ID would miss sub-agents and fresh threads after a restart.
- **Why the index, not walking `sessions/`:** a tree walk is O(all rollouts ever). The index is one indexed-by-scan query per binding and is the same source Argus already trusts for capture.
- **Alternative rejected:** parsing each rollout's `session_meta.cwd` by walking the tree. It is correct but costly, and it duplicates the index.

### D2. Price per call from `token_count` events, using the turn's recorded model

The parser walks new bytes of a rollout, tracking the most recent `turn_context.model`. For each `token_count` event with a non-null `last_token_usage` whose `total_token_usage` differs from the previous event's (Codex re-emits identical events), it builds one call:

- **Input:** `input_tokens - cached_input_tokens`, priced at the fresh-input rate.
- **CacheRead:** `cached_input_tokens`.
- **CacheWrite5m:** `cache_write_input_tokens`. Codex has a single cache-write class, so it maps to the 5m column and rate. The 1h column and rate stay unused for Codex models.
- **Output:** `output_tokens`. Reasoning tokens are already included, so they are not added again.

The rate key is the model Codex recorded, e.g. `gpt-6.1-sol`, not `agent.ResolveModel`'s alias. That is what was actually billed, and an empty Argus-side model for Codex tasks is common.

- **Alternative rejected:** pricing the delta of `total_token_usage` like the Claude transcript path. That cannot apply a per-call long-context tier, and it misattributes when the model changes mid-thread.

### D3. Long-context tier as an optional per-model rate block

`pricing.Rate` gains `LongContextThreshold int64` and `LongContext *Rate`. In TOML:

```toml
[models."gpt-6.1-sol"]
input = 2.00
cache_read = 0.10
cache_write_5m = 2.50
cache_write_1h = 2.50
output = 10.00
long_context_threshold = 272000

[models."gpt-6.1-sol".long_context]
input = 4.00
cache_read = 0.20
cache_write_5m = 5.00
cache_write_1h = 5.00
output = 15.00
```

A new `Table.PriceCall(model, deltas, callInputTokens)` uses the `long_context` rates when `threshold > 0 && callInputTokens > threshold`, otherwise the base rates. The comparison uses the call's total `input_tokens`, cached included, matching how the provider measures prompt size. The existing `PriceDelta` is unchanged, so the Claude path is unaffected.

### D4. Per-(binding, rollout) byte-offset cursor; accrual and cursor advance in one transaction

New table:

```sql
CREATE TABLE hera_codex_cost_cursor (
  binding_id   INTEGER NOT NULL REFERENCES hera_bindings(id) ON DELETE CASCADE,
  rollout_path TEXT    NOT NULL,
  byte_offset  INTEGER NOT NULL DEFAULT 0,
  last_total   INTEGER NOT NULL DEFAULT -1,  -- total_token_usage.total_tokens of the last consumed event (dedupe across sweeps)
  model        TEXT    NOT NULL DEFAULT '',  -- last turn_context model seen (carries across sweeps)
  PRIMARY KEY (binding_id, rollout_path)
);
```

Each sweep reads from `byte_offset` up to the last complete newline (a torn final line is left for next time), prices the new calls, and in ONE transaction adds the summed raw tokens and USD to the binding's columns and advances the cursor. A crash before commit re-reads the same bytes next sweep; a crash after commit never re-reads them. This keeps the existing "Accrual-time cost stamping" invariant: each call is priced once, at the rate in effect when first observed. A call whose model has no rate entry still advances raw tokens and the cursor but adds no USD, permanently, per the existing rule.

- **Alternative rejected:** re-summing the full rollout every sweep and diffing priced totals. That reprices history whenever `rates.toml` changes, violating immutability, and is O(file) per sweep on rollouts that reach hundreds of MB.

### D5. Attribute a call only to the binding whose window contains it

A call is accrued to a binding only if its event `timestamp` is ≥ `binding.started_at` and, when ended, < `binding.ended_at`. A new binding on a reused worktree gets fresh cursors at offset 0 but skips earlier calls by timestamp, so a call is never charged to two bindings. A recycled binding picks up the rollout's later calls, including a resumed thread appended to an old rollout.

### D6. Which bindings are swept: a family capability, plus a short grace after end

`agent.Family` gains `CostSource` (`CostSourceNone` default, `CostSourceClaudeHook`, `CostSourceCodexRollout`). The DB returns bindings with `ended_at IS NULL OR ended_at > now − 15m`. The sweeper keeps those whose task's family reports `CostSourceCodexRollout`, so it never branches on backend identity outside `backends.go`, per AGENTS.md.

The 15-minute grace window lets the final calls before a binding ended get read. A Claude binding is never touched, so there is no interaction with the transcript and bridge sources or their source-handoff logic in `hera_bridge_cost`.

### D7. Cadence: daemon goroutine, 60s tick plus once at startup

The sweeper is a sibling of `runHeraReclaimSweeper`: `d.done`-gated, panic-recovered, first sweep at startup. Steady-state cost is one SQLite query per live Codex binding plus reading only appended bytes. Each sweep logs bindings scanned, calls priced, calls unpriced (unknown model), USD added, and per-binding errors, under prefix `[codexcost]`. Per-binding failures are isolated, so a missing Codex home or unreadable rollout skips that binding without aborting the batch.

### D8. Backfill falls out naturally for live bindings only

Already-running orchestrators like sketch-reporting have live bindings (Codex workers keep live bindings after their task completes), so the first sweep accrues every call since each binding started. Bindings ended more than 15 minutes before the feature ships are not swept, consistent with the existing "no retroactive cost" rule. Their history is priced at the rates in effect at first observation, which is accrual-time stamping applied at the earliest moment the system could observe the calls.

## Risks / Trade-offs

- **[Risk] Codex changes its rollout or index schema.** → Parse defensively: skip unknown lines, treat a missing field as zero, log once per binding on a structural miss. A fixture test pins today's shape. The failure mode is under-counting, never a crash.
- **[Risk] Daemon sandbox or permissions can't read an account's Codex home.** → The daemon runs unsandboxed on the host, and capture already reads the same files. A read error is a per-binding skip, logged.
- **[Risk] A worktree path reused by an unrelated non-Argus Codex session in the same `cwd`.** → Accepted. Worktree paths are Argus-owned, and the binding-window timestamp filter bounds any bleed.
- **[Trade-off] Long-context rates are aggregator-sourced.** → Flagged in the seed comment. Operators hand-edit `rates.toml` (existing mechanism); edits affect only calls observed afterward.
- **[Trade-off] Existing installs keep their old `~/.argus/rates.toml`** (never-overwrite rule), so `gpt-6.1-sol` calls accrue raw tokens but no USD until the entry is added. → Covered in the migration plan. Doctor or log surfaces "unpriced model gpt-6.1-sol" via the sweep log's unpriced count.

## Migration Plan

1. Ship the change. The new table is created by the usual idempotent schema init; no data migration is needed.
2. On this machine, `~/.argus/rates.toml` already exists, so the `gpt-6.1-sol` seed is not installed automatically. Either append the seed block by hand or move the file aside so the daemon re-installs the new default on next start. Without this, Codex calls observed before the edit are permanently unpriced under the immutability rule. So do it before or immediately at deploy, before the first sweep.
3. Rollback: revert the binary. The extra table is inert, and accrued USD on Codex bindings stays in place (harmless, and accurate).

## Open Questions

- **Claude rates look stale.** The seed has Opus at $15 input / $75 output per million, which may be an older generation's pricing. It doesn't affect bridge-sourced Claude rows (engine USD) but does affect transcript-sourced ones. Correct it in this change, or as its own?
- **Other Codex models** (`gpt-6-sol`, `gpt-6-astra`, etc.): seed them now if published rates are confirmed during implementation, otherwise leave them for hand-editing.
