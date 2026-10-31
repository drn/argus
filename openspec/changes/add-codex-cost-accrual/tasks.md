## 1. Rate table: long-context tier and Codex seed

- [ ] 1.1 Add `LongContextThreshold` and `LongContext *Rate` to `pricing.Rate` (TOML `long_context_threshold`, `[models.X.long_context]`), with load tests for present, absent, and zero-threshold entries
- [ ] 1.2 Add `Table.PriceCall(model, deltas, callInputTokens)` choosing base vs long-context rates (tests: above, at, and below the threshold; no tier; unknown model returns ok=false); leave `PriceDelta` unchanged
- [ ] 1.3 Seed `gpt-6.1-sol` (standard and long-context) in `internal/pricing/rates.toml`, replacing the "Codex deliberately not seeded" comment with sourcing notes (long-context and cache-write figures flagged as aggregator-sourced)

## 2. Backend family cost-source capability

- [ ] 2.1 Add `CostSource` to `agent.Family` (`None` / `ClaudeHook` / `CodexRollout`); set claude/custom → `ClaudeHook`, codex → `CodexRollout`, others → `None`; registry test asserting each family's value
- [ ] 2.2 Add the capability column to `context/knowledge/backends.md`'s matrix

## 3. Rollout parser (`internal/codexcost`)

- [ ] 3.1 Add a real-shape testdata rollout fixture (trimmed and anonymized: `session_meta`, `turn_context` model switch, duplicate `token_count`, null-info `token_count`, torn final line)
- [ ] 3.2 Implement `ParseFrom(r io.ReaderAt, offset, lastTotal, model)` → calls (with timestamp, model, mapped deltas, total input) plus new offset, lastTotal, and model; stop at the last complete newline; skip malformed or unknown lines
- [ ] 3.3 Tests: duplicate de-dupe (within and across calls via lastTotal), uncached-input math, reasoning not added, model carried across calls and from the cursor, torn line deferred, empty and garbage input
- [ ] 3.4 Implement thread discovery: read-only `state_5.sqlite` `SELECT id, rollout_path FROM threads WHERE cwd = ?` in a given Codex home; tests against a temp SQLite with a sub-agent row and a foreign-cwd row

## 4. Persistence

- [ ] 4.1 Add the `hera_codex_cost_cursor` table to schema init (idempotent; FK cascade on binding delete)
- [ ] 4.2 Add `ListCostSweepBindings(graceSince time.Time)` returning bindings that are live or ended after graceSince, with task id, worktree, started_at, and ended_at
- [ ] 4.3 Add `AccrueCodexCalls(bindingID, rolloutPath, Δtokens, Δusd, newCursor)` that adds to the binding columns and upserts the cursor in one transaction, plus `GetCodexCostCursor`; tests for atomicity (rollback leaves both untouched), cascade, and ErrHeraNotFound

## 5. Sweeper

- [ ] 5.1 Implement `codexcost.Sweep(ctx, deps)`: per binding, filter by family `CostSource == CodexRollout`, resolve the Codex home, discover threads, parse each rollout from its cursor, keep calls inside the binding window, price each with `PriceCall` at the current table (repo-then-library precedence as in `heraRateTable`), and commit via `AccrueCodexCalls`; per-binding error isolation
- [ ] 5.2 Tests (temp HOME, fake Codex home, in-memory DB): first sweep accrues the expected USD; second sweep adds zero; appended calls accrue incrementally; unpriced model advances tokens only; window filter on rebinding; Claude binding untouched; missing Codex home skips and logs; long-context call priced at tier
- [ ] 5.3 Wire `runCodexCostSweeper` into the daemon (startup sweep plus 60s ticker, `d.done`-gated, panic-recovered), with a test seam so daemon tests never read a real `~/.codex`
- [ ] 5.4 Log per sweep under `[codexcost]` (bindings, calls priced and unpriced, USD added, errors) via slog/uxlog per daemon convention

## 6. Docs and gate

- [ ] 6.1 Add gotchas to `context/knowledge/gotchas/misc.md`, or a cost section: cwd-not-session-id discovery, cached ⊂ input and reasoning ⊂ output, de-dupe on identical totals, cursor and accrual single transaction, binding-window attribution, existing `rates.toml` never overwritten (manual seed step); update the index row
- [ ] 6.2 Update the README Reference section if it documents cost sources or `rates.toml`
- [ ] 6.3 `make pre-pr` green
- [ ] 6.4 Archive the change (`openspec archive add-codex-cost-accrual`, or a manual merge-and-move) in the same PR
