## Context

Argus records per-role token telemetry as a live snapshot, not a history: the `argus coord-hook` `Stop`-hook subcommand (`cmd/argus/coord_hook.go:719`, `scanContextSize`) parses the Claude Code transcript JSONL once per turn and OVERWRITES a single `task_meta(hera, context_size)` value with the LATEST main-chain assistant message's `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` (coord_hook.go:742-758). Output tokens are never read. There is no running total, no per-turn history, and no cost concept anywhere in the codebase — the only prior "cost" reference is a one-off comment in `internal/llm/namegen.go:7,132` justifying a timeout constant (~$0.0034/call at Haiku 4.5 pricing), not a reusable pattern.

Duration is already solid (`tasks.created_at/started_at/ended_at`, `hera_bindings.started_at/ended_at/end_reason`), and the rollup path already exists for other per-subtree metrics: `Model.SubtreeAgentCount` (`internal/tui/hera/model.go:811`) walks `Model.BridgeSubtree(orchID)` to sum a metric across a coordinator's whole nested subtree without double-counting a nested sub-coordinator's agent (counted once, via the bridging worker row). This design reuses that exact walk for cost.

Model/backend per role is resolved live, not stored on `hera_roles` (which has no model column): `internal/tui/hera_tiering.go:31-98` (`resolveHeraTier`) calls `agent.ResolveModel` (`internal/agent/agent.go:129`) to fill `RoleView.AppliedModel`. `agent.KnownModels` (agent.go:213-227) returns CLI ALIASES, not versioned model IDs — `"opus"/"sonnet"/"haiku"/"fable"` for Claude, `"gpt-5-codex"/"gpt-5"` for Codex; opencode/Pi/custom backends return `nil` (no curated list).

Three frontends read hera data today: the TUI native Hera view (`internal/tui/hera/`), the web SPA's Hera tab (`internal/api/static/`, fed by `GET /api/hera`), and the macOS app's read-only Hera tab (`macos/Sources/ArgusKit/Models+Hera.swift`, same endpoint). CLAUDE.md's Frontend Parity rule requires evaluating any user-facing/REST-exposed change against all three in the same PR, or an explicit named follow-up if deferred.

> **Revision note:** Decisions 2 and 3 below were corrected after coordinator/Aaron review (hera message #4764). The original draft computed USD live at display time against "the current rate table," which Aaron flagged as wrong: a later rate change must never retroactively shift an already-recorded historical cost. The corrected mechanism (accrual-time stamping, Decision 2) and a resolved TTL question (Decision 3) are reflected below; superseded reasoning is called out explicitly rather than silently deleted, so the "why" of the correction stays visible.
>
> **Second revision note:** all five Open Questions are now resolved (hera message #4770). Decision 3's rate-table HOME changed again — from "embedded Go map + `config.toml` override" to a committed seed `rates.toml`, mirroring the diligence-profiles seed/install/precedence pattern exactly (no config-override layer, no live-reload machinery). Decision 5's UI scope narrowed to TUI-only for this change, with the web SPA and macOS Hera tabs named as an explicit deferred follow-up (mirroring the existing "hera mutations are TUI-only" precedent) rather than shipped silently-incomplete. A new UI-detail decision (Q5) confines rendering to the blended total, never the five-class raw breakdown, though the breakdown remains stored and REST-exposed.

## Goals / Non-Goals

**Goals:**

- Deterministically estimate the dollar cost a hera coordinator's whole subtree (itself + every nested worker/sub-coordinator) has accrued, from real token usage already visible in Claude Code transcripts.
- Extend the existing per-turn transcript parse rather than add new instrumentation.
- Beat a duration × hourly-rate proxy on accuracy by pricing actual input/cache/output tokens per model.
- Price each slice of usage at the rate in effect when it was incurred — a later rate-table correction must never retroactively change an already-recorded historical cost.
- Surface a blended cost figure on the TUI Hera rail/details pane for this change (Decision 5); the underlying data stays REST-exposed so the deferred web/macOS follow-up (below) can reuse it without further backend work.

**Non-Goals:**

- Retroactive cost for roles/bindings that ran before this ships, OR for any accrual period whose model had no rate-table entry at the time (accrual is priced only as it happens — see Decision 6).
- Exact accounting reconciled against Anthropic's actual invoice (this is an ESTIMATE — see Risks for the specific approximations).
- Cost for non-Claude backends beyond whatever `KnownModels` already curates (Codex's two aliases); opencode/Pi/custom models show no cost figure, not a fabricated one.
- Any new mutation surface (setting/editing rates from the UI, disputing a cost figure, budgets/alerts on cost) — this change is read-only telemetry, same posture as the existing REST hera roster.
- Rebuilding `scanContextSize`'s own retry-and-max-across-scans logic for the raw-token-count path — see Decision 1 for why a sum doesn't need it.
- A `config.toml`-based rate override, or any live-reload mechanism for rates — superseded by Decision 3's seed-file approach, which needs neither.
- **Web SPA and macOS Hera-tab cost rendering** (decided, Open Question 4 resolved per Aaron, hera message #4770): this change is TUI-only. Per CLAUDE.md's Frontend Parity rule, this is an explicit NAMED gap, not silence — mirroring the existing standing exception that "hera mutations are TUI-only; over REST hera is read-only." `GET /api/hera`'s new fields still populate (Decision 5) so a future follow-up change can add rendering to both without further backend work; this is the named follow-up itself, tracked here rather than as a separate change yet.
- **A raw per-rate-class token/cost breakdown rendered in any UI** (decided, Open Question 5 resolved per Aaron, hera message #4770): only the blended `cost_usd`/`subtree_cost_usd` total renders anywhere. The five raw token-count columns and the per-accrual pricing detail still exist in the DB and are still exposed via `GET /api/hera` (useful for debugging and for the deferred web/macOS follow-up) — this Non-Goal governs rendering only, not storage or REST exposure.

## Decisions

### Decision 1: Sum every turn's usage across the whole transcript on every hook call, not "stop overwriting and start adding"

`scanContextSize` (coord_hook.go:719-764) scans every non-sidechain `"assistant"` line and OVERWRITES a local `size` variable each time (line 758) — so the function's return value is the LATEST turn's total context, a high-water-mark snapshot, not a sum. This matters because it's tempting to misread "accumulate into a running total" as "just don't overwrite, add instead" — that would be wrong for context_size, whose whole point IS the latest snapshot (a context-window gauge).

Token *counts* are different: Anthropic bills EVERY API call for its own full usage (fresh input + cache-write + cache-read + output), independent of what any other turn already paid for. The correct total-usage computation is the SUM of every turn's own usage, not just the latest. So this change adds a NEW scan function (e.g. `scanTokenTotals`) that keeps a running sum across all qualifying lines instead of overwriting, reading, per line: `input_tokens`, `cache_read_input_tokens`, `output_tokens` (the latter never read by `scanContextSize` today), and — per Decision 3's confirmed finding — the nested `usage.cache_creation.ephemeral_1h_input_tokens` / `ephemeral_5m_input_tokens` fields in place of the flat `cache_creation_input_tokens` `scanContextSize` reads, since those two sub-fields are what let cache-writes be priced by TTL tier instead of blended.

Each hook invocation re-scans the FULL transcript and OVERWRITES the persisted RAW TOKEN COUNTS with the fresh sums — it does NOT do `stored_total += this_turn's_tokens`. Rationale: a transcript is append-only, so a fresh full resum is idempotent (re-running it against an unchanged transcript reproduces the same totals) and naturally monotonic (a later scan can only see the same or more complete lines, never fewer) — unlike an incremental `+=`, which would double-count if the Stop hook ever fired twice for the same turn. This also means the retry-and-take-max dance `readContextSizeReal`/`scanContextSize` need (to catch `transcript_path`'s async-write lag) is NOT needed here: an under-count from a transcript write that hasn't landed yet just self-corrects on the NEXT hook call, with no risk of ever over-counting.

**This full-resum-overwrite mechanism applies to the RAW TOKEN COUNTS only.** The persisted DOLLAR figure is a separate, incrementally-accrued value with its own update rule — see Decision 2, which was corrected specifically because treating the dollar figure the same way (recompute-and-overwrite against "whatever the rate table currently says") turned out to violate a correctness requirement Decision 1's own reasoning does not need to satisfy for plain counts.

### Decision 2: Persist raw per-rate-class token totals on `hera_bindings`, and accrual-time-stamp a separately persisted dollar total — REVISED, historical cost must never retroactively shift

**What changed and why:** the original draft of this decision computed USD live, at display/rollup time, by multiplying the stored raw token totals against "the current rate table" — and named that as a deliberate, positive property (a rate correction would immediately reprice every historical binding). Aaron's review (hera message #4764) flagged this as wrong: if a model's price changes, already-recorded PAST cost must not shift — e.g. a price doubling must not double an already-computed historical figure. Cost has to be computed and persisted AT ACCRUAL TIME, using the rate in effect at that moment, never re-derived later from whatever the rate table says when someone happens to look. The mechanism below replaces the old read-time computation; the raw-token-persistence rationale (why `hera_bindings`, not `task_meta`) is unchanged.

**Where totals live, and why not `task_meta`:** the mission's originally suggested location was `task_meta` (mirroring `context_size`). Investigating that path surfaced a blocking problem: `DB.SetArchived(id, archived=true)` (`internal/db/tasks.go:293`) calls `DeleteMetaForTask` (`internal/db/task_meta.go:161-171`), which deletes EVERY `task_meta` row for that task. Archiving a task is a routine, expected lifecycle event for a finished hera worker — so storing token totals in `task_meta` would mean a coordinator's rollup total silently drops every archived child's recorded spend the moment it's archived. `hera_bindings` (schema.go:546-554) has no such archive-triggered deletion and already holds the equivalent durable per-incarnation facts (`started_at`/`ended_at`/`end_reason`) for exactly this reason.

This design adds to `hera_bindings`:

- Five raw token-count columns (INTEGER, default 0, full-resum-overwrite per Decision 1): `tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output`.
- One persisted dollar column: `cost_usd_accrued` (REAL, default 0) — INCREMENTALLY accumulated, never wholesale overwritten, never re-derived from a later rate table.

**Accrual mechanism (the corrected part):** the client side (the hook, `coord_hook.go`) is unchanged in spirit from Decision 1 — it re-scans the full transcript and computes fresh raw totals, then POSTs them to a new hera-scoped REST endpoint (e.g. `PUT /api/hera/bindings/current/tokens`, resolved server-side from the caller's `ARGUS_TASK_ID` to its currently-live binding). The daemon-side handler — which already owns the rate table (Decision 3) as a server-side, single-source-of-truth concern — does the pricing:

1. Read the binding's PREVIOUSLY persisted five raw totals and its previous `cost_usd_accrued`.
2. Take the freshly-POSTed five raw totals (this invocation's full resum).
3. Compute `delta[class] = new[class] - previous[class]` for each of the five rate classes (a transcript is append-only, so this should never be negative; clamp to 0 defensively rather than subtracting cost).
4. Look up the CURRENT rate table (as of THIS invocation, resolving the role's live `AppliedModel` the same way `resolveHeraTier` already does) and compute `delta_cost = Σ delta[class] × rate[class]`.
5. Write, atomically: the new five raw totals, AND `cost_usd_accrued = previous_cost_usd_accrued + delta_cost`.

This is deliberately NOT "recompute the whole dollar total from scratch and overwrite" (Decision 1's pattern for raw counts) — it is a genuine incremental accumulator, because that is the only way to guarantee a later rate change affects only future deltas, never past ones already priced and folded into `cost_usd_accrued`.

**Why this stays safe against a duplicate/retried Stop event** (the exact failure mode Decision 1 designed around for raw counts): if the hook fires twice against an unchanged transcript, the second invocation's freshly-resummed raw totals equal what was already persisted from the first invocation, so every `delta[class]` is 0, so `delta_cost` is 0, so `cost_usd_accrued` is unchanged. The idempotency property Decision 1 established for raw counts is preserved for the dollar figure too — just via zero-delta rather than overwrite-with-same-value.

**Corollary to Decision 6 (no retroactive cost):** if a role's resolved model has NO rate-table entry at the moment a delta is observed, that slice of usage's cost is permanently unpriced — it is never recovered retroactively even if a rate for that model is added later, because there is no stored per-period token breakdown to reprice, only the already-folded-in accrued total. Raw token counts still advance normally regardless (they carry no pricing assumption); only the dollar figure has gaps.

A role can be re-bound/recycled (multiple incarnations over its life). Per-ROLE total = sum of `cost_usd_accrued` across ALL of `ListHeraBindingsByRole(roleID)` (`internal/db/hera.go:1358-1364`, already returns every incarnation, live and ended) — pure addition of already-priced numbers, no rate lookup needed at this layer at all.

**Alternative considered and rejected (this is the one that flipped):** computing USD live at read/rollup time from stored raw counts against "the current rate table" — the ORIGINAL design of this decision. Rejected per Aaron's review: it makes every historical cost figure retroactively drift whenever the rate table changes, which is factually wrong (past usage was billed at the rate that applied when it was incurred, not today's rate). The previously-stated upside ("a rate correction instantly reprices every historical binding with no backfill") was actually describing the bug, not a feature.

### Decision 3: Rate table has FIVE classes, not four — cache-write TTL is deterministically parseable from the transcript, not blended — REVISED, confirmed via direct inspection

**What changed and why:** the original draft accepted "one blended cache-write rate" as an approximation, reasoning that the transcript doesn't distinguish which cache TTL (5-minute vs 1-hour) applied to a given cache-write. Coordinator research into Claude Code's own behavior (relayed via hera message #4764) found the TTL itself is fully deterministic — auto-selected from (auth mode + main-session-vs-subagent): a Claude-subscription-authenticated main session defaults to 1-hour TTL (dropping to 5-minute if drawing on metered credits, since 1-hour writes cost more once metered), an API-key/cloud-provider session defaults to 5-minute unless `ENABLE_PROMPT_CACHING_1H=1` is set, and a native Claude sub-agent's own turns ALWAYS use 5-minute regardless of the parent session's auth mode. That alone would only narrow "blend" to "infer from auth context," which is still an approximation — so the remaining question was whether the transcript actually records which tier applied, rather than requiring that inference.

**Confirmed by direct inspection, not guessed:** sampling this session's own transcript (`~/.claude/projects/-Users-aaron--argus-worktrees-ARGUS-cost-estimate-proposal/49bf90b0-9cc6-4091-ac42-c157ebf1187d.jsonl`) shows every assistant message's `usage` object carries a nested `cache_creation` object with EXACTLY this shape:

```json
"usage":{"input_tokens":2,"cache_creation_input_tokens":1285,"cache_read_input_tokens":265459,"output_tokens":1740,...,"cache_creation":{"ephemeral_1h_input_tokens":1285,"ephemeral_5m_input_tokens":0}}
```

Confirmed across multiple sampled lines that the flat `cache_creation_input_tokens` field (the one `scanContextSize` already parses) is EXACTLY the sum of the nested `ephemeral_1h_input_tokens` + `ephemeral_5m_input_tokens` fields (e.g. `1285 = 1285 + 0`, `2090 = 2090 + 0`, `238 = 238 + 0` — this session runs 1-hour TTL throughout, consistent with subscription auth on the main conversation). So no inference from auth mode is needed at all: `scanTokenTotals` (Decision 1) reads `usage.cache_creation.ephemeral_1h_input_tokens` and `usage.cache_creation.ephemeral_5m_input_tokens` directly as two separate running totals, in place of the single flat field.

The rate table therefore needs FIVE rates per model, not four: fresh input, cache-write-1h, cache-write-5m, cache-read, output — keyed by the same model-alias strings `agent.ResolveModel`/`RoleView.AppliedModel` already produce (`agent.KnownModels`, agent.go:213-227; `resolveHeraTier`, hera_tiering.go:96-97), reused as-is with no new model-resolution path. Pricing itself happens server-side, at accrual time (Decision 2's step 4), where the rate table is the single source of truth.

**Defensive fallback, not the primary path:** a transcript line whose flat `cache_creation_input_tokens` is nonzero but whose nested `cache_creation` object is absent (a hypothetical older/different transcript shape, not observed in the sampled data above) is treated as an accepted approximation attributed to the 5-minute tier — the non-opted-in default — rather than blocking accumulation entirely.

**Rate table home — REVISED again (Open Question 2 resolved, Aaron, hera message #4770):** not an embedded Go map with an open `config.toml`-override question, but a committed seed file mirroring the diligence-profiles seed/install/precedence pattern EXACTLY, rather than inventing a new shape:

- A `rates.toml` file is authored and committed in the repo (e.g. `internal/pricing/rates.toml`), `//go:embed`-ed as a single seed default — the same shape as `internal/profiles/seeds.go`'s `seedFS`/`SeedNames`, just one file instead of three.
- An install function mirroring `profiles.InstallDefaults` (`internal/profiles/install.go:15-35`) writes the embedded seed to `~/.argus/rates.toml` ONLY if that path doesn't already exist — it never overwrites an operator's hand-edited copy. `InstallDefaults` itself is only ever invoked explicitly (a Settings UI action, `internal/tui/app.go:1508`) — never automatically at daemon startup. Rates differ from profiles here: pricing data is required infrastructure for an always-on background accrual mechanism (the Stop hook), not an opt-in customization a user deliberately turns on, so this design calls the install function automatically and idempotently (e.g. at daemon startup, a no-op if the file already exists) rather than gating it behind a manual Settings action — a deliberate, reasoned divergence from the mirrored pattern's call site, not the storage/lookup mechanism itself.
- Lookup precedence mirrors `profiles.Loader.locate` (`internal/profiles/load.go:33-47`): an in-repo copy (e.g. `<worktree>/.argus/rates.toml`) takes precedence over the installed library copy (`~/.argus/rates.toml`) when present, letting a specific project pin custom test rates without touching the shared library file.
- **No live-reload mechanism is needed, and none is built:** `profiles.Loader.Load` (`internal/profiles/load.go:91-105,111-113`) has no caching layer at all — every lookup calls `toml.DecodeFile` fresh from disk. Reusing this exact pattern for rates means a hand-edit to `~/.argus/rates.toml` takes effect on the very next accrual-time pricing lookup (Decision 2, step 4), with no mtime-watch or reload trigger to build — this is why no live-reload requirement was needed here, unlike `config.toml`'s override layer elsewhere in the project, which DOES need one because it caches.

A model outside the curated set (opencode/custom/Pi backends, which return `nil` from `KnownModels`), or simply absent from `rates.toml`, has no rate entry and accrues no cost figure for that period — surfaced as "n/a", not a fabricated guess (Decision 6's corollary).

**Named risk, reframed for accrual-time pricing:** a CLI alias like `"sonnet"` always resolves to "whatever Anthropic currently designates," so if Anthropic rotates the underlying model version at a different price, newly-accrued deltas are priced at the STALE rate until someone notices and hand-edits `~/.argus/rates.toml` (or the in-repo override copy). Because pricing is now accrual-time-stamped (Decision 2), this mispricing is confined to the window between the actual price change and the correction — it can no longer cascade into retroactively corrupting history, but the live window itself is still a real risk, and there is no live-reload watcher to surface that the table has gone stale. See Risks.

### Decision 4: Per-coordinator total mirrors `SubtreeAgentCount`'s double-count-safe walk, but sums every role kind's ALREADY-PRICED cost and bypasses the `nuked_at` exclusion

`Model.SubtreeAgentCount` (`internal/tui/hera/model.go:811-821`) already solves "sum a metric across a coordinator's nested subtree without double-counting a nested sub-coordinator" by walking `m.BridgeSubtree(orchID)` and counting worker-kind roles only (deliberately excluding each subtree's own coordinator role, folded into its header, to avoid counting a nested sub-coordinator twice). This design's subtree-cost rollup reuses the same walk but sums ALL role kinds (coordinators accrue real cost too) while preserving the exact same "count a nested sub-coordinator's spend exactly once, via its bridging representation" invariant `SubtreeAgentCount` already established.

Because `cost_usd_accrued` is already fully priced at write time (Decision 2), this rollup is PURE ADDITION at read time — summing an already-priced dollar column across bindings and roles — with no rate-table lookup and no live repricing happening anywhere in the rollup path. This is simpler than the originally-drafted version of this decision, which needed to join token totals against a live rate table at rollup time.

The `nuked_at IS NULL` exclusion baked unconditionally into `ListHeraRoles` (`internal/db/hera.go:557-568`, specifically line 564, confirmed to apply even when `includeArchived=true`) is correct for DISPLAY (a nuked role is fully torn down and hidden everywhere) but wrong for a financial rollup — money genuinely accrued by a since-nuked child must still count, or the total silently under-reports every time a coordinator nukes a finished child, which is normal cleanup. **Decided (Aaron, hera message #4764, Open Question 1 resolved):** this is implemented via a NEW, dedicated DB query path (e.g. `ListHeraRolesForCostRollup`) rather than an `includeNuked` parameter added to `ListHeraRoles` itself — `ListHeraRoles`'s existing signature and behavior stay untouched for every other caller.

### Decision 5: TUI-only rendering for this change, with an explicit named follow-up for web SPA and macOS — DECIDED (Open Question 4 resolved, Aaron, hera message #4770)

CLAUDE.md's Frontend Parity rule requires either shipping a user-facing/REST-exposed change on all three frontends in the same PR, or an explicit named follow-up if deferred — never silence. Aaron chose the deferral: this change renders cost ONLY in the TUI. Every figure surfaced below is the PERSISTED, already-priced `cost_usd_accrued` sum (Decision 2/4) — never a value recomputed live against "the current rate table" at display time.

- **REST** (`internal/api/hera.go`): STILL extended — `heraRoleJSON` (lines 15-27) gains per-role token totals and `cost_usd`, and `heraOrchJSON` (lines 32-43) gains a `subtree_cost_usd` field (the Decision-4 rollup). This is not optional scope-creep: the native TUI itself reads through this same REST surface in `--remote` mode (per the `remote-tui` capability's `apistore`/`apiclient` architecture), so the fields are load-bearing for TUI-only rendering, not just a courtesy to the deferred clients. Shipping them now also means the named follow-up below needs no further backend work.
- **TUI**: the natural fit is NOT the rail's per-role row — that row is already width-squeezed (the existing "Worker/freelance rail rows show a context-pressure indicator" requirement reserves an exact 2-character trailing slot and truncates the role name to make room). Instead, extend the orchestrator header's existing right-aligned bare-number agent-count badge (the "Orchestrator and role row rendering (area 3)" requirement, `internal/tui/hera/rail.go` `drawOrchRow`) with the subtree cost total, and surface the per-role figure in the details pane rather than the rail row. Per Decision 7 below, both render ONLY the blended total — no per-rate-class breakdown.
- **Web SPA and macOS: explicitly NOT in this change.** Named here as the required follow-up (mirroring the existing standing exception "hera mutations are TUI-only; over REST hera is read-only") rather than a silent gap — see the Non-Goals bullet added for this. The REST fields above are already in place for whenever that follow-up lands.

Cost is display-only on every surface — no editing rates, no per-role cost mutation, matching the existing read-only REST hera posture.

### Decision 6: No retroactive cost; zero/unset is the "not yet measured" signal

A binding whose session ran (and possibly ended) before this ships never had its transcript scanned for token totals, so its columns default to and stay 0. Since every REAL session incurs nonzero token usage, "all five raw columns are exactly 0 AND `cost_usd_accrued` is 0" is an unambiguous "never measured" signal at display time (rendered as "n/a", not "$0.00") — no separate NULL/sentinel column is needed.

Per Decision 2's corollary, this now also covers a SECOND case beyond pre-ship history: any accrual period whose role had no matching rate-table entry at the time, whose usage is permanently excluded from `cost_usd_accrued` even if a rate for that model is added later (raw token counts still advance regardless — only the dollar figure has gaps). Both cases render identically as "unmeasured," and neither is ever backfilled.

### Decision 7: Blended total only in the UI — DECIDED (Open Question 5 resolved, Aaron, hera message #4770)

The TUI renders only the single blended `cost_usd` / `subtree_cost_usd` figure — never the five raw rate-class token counts or their individual priced contributions. This governs RENDERING only: the five raw columns and `cost_usd_accrued` remain fully persisted (Decision 2) and fully exposed via `GET /api/hera` (Decision 5) regardless of this choice, so a future UI (including the deferred web/macOS follow-up, or a debugging curl against the REST endpoint) can still see the breakdown even though the TUI itself does not render it.

## Risks / Trade-offs

- **[Risk]** Alias-keyed rate table (Decision 3) goes stale when Anthropic rotates which model version underlies a CLI alias (e.g. `"sonnet"`) at a different price — now confined to a LIVE mispricing window rather than a retroactive one (Decision 2's accrual-time stamping prevents it from corrupting history), but the window itself is still real, and there is no live-reload watcher to surface that the table has gone stale. → **Mitigation:** correcting `~/.argus/rates.toml` (or an in-repo override copy) takes effect on the very next accrual-time lookup with no restart needed (Decision 3's no-caching property); document the alias-not-version caveat prominently so a "wrong-looking NEW cost" after a model swap isn't mistaken for a logic bug.
- **[Risk]** Custom/opencode/Pi-backend models have no curated identifier (`KnownModels` returns `nil`) and so no rate entry, ever, for any accrual period. → **Mitigation:** cost shows "n/a" for those roles rather than a fabricated guess; explicitly a Non-Goal to cover them.
- **[Risk]** The accrual mechanism (Decision 2) requires a read-modify-write on the daemon side (read previous totals + previous accrued cost, compute a delta, write both) rather than `context_size`'s simpler blind overwrite — a more complex REST handler contract than any existing hera-hook endpoint. → **Mitigation:** confined entirely to the one new endpoint; proven safe against duplicate/retried hook fires via the zero-delta argument in Decision 2; no change to the existing `context_size` path.
- **[Risk]** The new cost-rollup DB query path deliberately includes nuked roles, diverging from every other existing hera rollup (agent count, needs-input rollup, etc.), which all exclude them (Decision 4). → **Mitigation:** implemented as a wholly separate function (decided, Open Question 1 resolved), not a change to `ListHeraRoles`'s existing behavior/callers; the divergence itself needs this doc's rationale so a future reader doesn't "fix" it back into consistency with the display rollups.
- **[Risk]** Full-transcript resum on every Stop-hook call grows linearly with conversation length. → **Mitigation:** none needed beyond the existing precedent — `scanContextSize`'s own doc comment (coord_hook.go:709-710) already accepts this cost class, reasoning the HTTP round-trip dominates; the raw-count path is actually CHEAPER than context_size's, since it needs no retry-and-max loop (Decision 1).
- **[Risk]** A new REST write path is additional daemon-side surface that will 405 on a stale-binary daemon until rebuilt+restarted (the existing `hera_send 405` class of issue). → **Mitigation:** none beyond the existing rebuild-and-restart playbook; noted so implementers don't chase a phantom bug.
- **[Risk]** Installing `rates.toml` automatically at daemon startup (Decision 3's deliberate divergence from the profiles precedent's manual-only trigger) means the install path runs unattended, unlike every other consumer of the `InstallDefaults` shape. → **Mitigation:** the install function's own existing contract (never overwrite an existing file) makes repeated automatic calls safe/idempotent by construction; no new safeguard needed beyond reusing that contract as-is.
- **[Risk]** Deferring web SPA and macOS rendering (Decision 5) means an operator using either of those surfaces sees no cost figure at all, even though the REST data exists. → **Mitigation:** named explicitly as a Non-Goal + follow-up per CLAUDE.md's Frontend Parity rule, not silent; the REST fields ship now so the follow-up needs no backend work.

## Migration Plan

New `hera_bindings` columns (five raw INTEGER token-count columns plus `cost_usd_accrued` REAL) land via the schema's existing idempotent self-evolving pattern (CREATE-TABLE-carries-column-inline for fresh DBs; ALTER for existing ones) — no explicit migration script. A new committed `rates.toml` seed file (e.g. `internal/pricing/rates.toml`) ships in the repo, `go:embed`-ed and installed to `~/.argus/rates.toml` on daemon startup if absent (Decision 3) — no config-file migration, no schema for it beyond the TOML file itself. No backfill for historical bindings or for any accrual period whose model lacked a rate entry at the time (Decision 6) — additive only, consistent with the project's no-legacy-migration-code policy.

Suggested build order (detailed in `tasks.md`): (1) `rates.toml` seed + install/lookup mechanism (five classes); (2) hook raw-token-count accumulation (five classes) + `hera_bindings` columns + the new REST endpoint's read-modify-write accrual handler; (3) rollup queries (per-role, per-coordinator, including the nuked-inclusive carve-out — now pure addition); (4) REST DTO fields; (5) TUI render (blended total only, per Decision 7). Web SPA and macOS rendering are explicitly OUT of this build order (Decision 5) — a separate future change.

## Open Questions

All five are now resolved (Aaron, hera messages #4764 and #4770); kept here for traceability rather than deleted.

1. ~~**Nuked-inclusive rollup query shape**~~ — **RESOLVED**: a dedicated new DB function, not an `includeNuked` parameter on `ListHeraRoles`. See Decision 4.
2. ~~**Rate table home**~~ — **RESOLVED**: not an embedded Go map with a `config.toml` override, but a committed seed `rates.toml` mirroring the diligence-profiles seed/install/precedence pattern, with no live-reload mechanism (none needed — see Decision 3).
3. ~~**Cache-write TTL granularity**~~ — **RESOLVED**: the transcript's nested `usage.cache_creation.ephemeral_1h_input_tokens`/`ephemeral_5m_input_tokens` fields give an exact, deterministic per-TTL breakdown (confirmed via direct inspection — see Decision 3). No blending needed; a flattened-only fallback is kept as defensive-only handling for an unobserved transcript shape.
4. ~~**UI parity scope**~~ — **RESOLVED**: TUI-only for this change; web SPA and macOS are an explicit named follow-up, not silence. See Decision 5.
5. ~~**Per-role token breakdown visibility**~~ — **RESOLVED**: only the blended total renders in the UI; the raw breakdown stays stored and REST-exposed for a future consumer. See Decision 7.

**No implementation work has started. `tasks.md` is not yet approved to execute — Aaron still needs to give explicit go-ahead after reviewing this updated design**, per the coordinator's message resolving these questions.

## Acceptance criteria

**Decision 1 (token-count scan):**

- it should sum `input_tokens` + `cache_read_input_tokens` + `output_tokens` + the two TTL-split cache-write fields across every non-sidechain main-chain assistant message in the transcript, not just the latest one
- it should exclude `isSidechain: true` lines from the sum, mirroring `scanContextSize`'s existing exclusion
- it should produce the same raw totals when re-run twice against an unchanged transcript (idempotent)
- it should produce raw totals at least as large as any prior scan of a transcript that has only grown (monotonic, no regressions from partial reads)

**Decision 2 (persistence on hera_bindings; accrual-time dollar stamping):**

- it should persist the five raw running token totals as columns on the binding's `hera_bindings` row, keyed to the task's currently-live binding
- it should leave a binding's raw totals and `cost_usd_accrued` unchanged when its underlying task is archived
- it should compute a role's total by summing `cost_usd_accrued` across every `ListHeraBindingsByRole` row (live and ended) — pure addition, no rate lookup
- it should price only the DELTA between the newly-observed raw totals and the previously-persisted raw totals, against the rate table AS OF THE MOMENT the delta is observed, and add that priced delta to `cost_usd_accrued`
- it should leave `cost_usd_accrued` unchanged when a duplicate/retried hook invocation observes no new raw-total delta
- it should leave every already-accrued `cost_usd_accrued` value unchanged when the rate table later changes — only future deltas use the new rate
- it should never assign a cost, even after a rate is later added for that model, to a delta that was observed while no rate-table entry existed

**Decision 3 (five-class rate table, TTL-split, seed-file mechanism):**

- it should look up rates by the same model-alias string already resolved into `RoleView.AppliedModel`
- it should read `usage.cache_creation.ephemeral_1h_input_tokens` and `usage.cache_creation.ephemeral_5m_input_tokens` as two separate running totals, not the flattened `cache_creation_input_tokens`
- it should apply distinct rates for fresh-input, cache-write-1h, cache-write-5m, cache-read, and output tokens
- it should surface no cost figure (not a zero, not a guess) for a model absent from the rate table at accrual time
- it should never overwrite an existing `~/.argus/rates.toml`, mirroring `InstallDefaults`'s existing never-overwrite contract
- it should prefer an in-repo `rates.toml` copy over the installed library copy when both are present
- it should reflect a hand-edit to `rates.toml` on the very next accrual-time lookup, with no restart and no explicit reload call

**Decision 4 (subtree rollup):**

- it should sum a coordinator's own `cost_usd_accrued` together with every nested worker's and sub-coordinator's, walking the same `BridgeSubtree` traversal `SubtreeAgentCount` uses
- it should count a nested sub-coordinator's cost exactly once, not twice, via the same bridging-row convention `SubtreeAgentCount` already established
- it should include a nuked child role's recorded cost in its coordinator's subtree total, via the dedicated new query path
- it should exclude a nuked child role from every OTHER existing rollup's behavior (agent count, needs-input) unchanged — this design touches only the new cost-rollup query path

**Decision 5 (TUI-only, REST stays extended):**

- it should expose per-role token totals and `cost_usd`, and per-orchestrator `subtree_cost_usd`, on `GET /api/hera`, all sourced from persisted accrued values
- it should render the subtree cost total on the TUI orchestrator header alongside the existing agent-count badge
- it should render NO cost figure on the web SPA's Hera tab or the macOS app's Hera tab in this change (explicitly deferred, not silently missing — see the Non-Goals bullet)
- it should expose no control on any surface that edits a rate, edits a recorded token total, or otherwise mutates cost data

**Decision 6 (no retroactive cost):**

- it should render "n/a" (not "$0.00") for a binding whose raw columns and `cost_usd_accrued` are all still 0
- it should apply no backfill or migration to bindings that predate this change, or to any accrual period whose model lacked a rate entry at the time

**Decision 7 (blended total only in the UI):**

- it should render only the single blended cost figure in the TUI (orchestrator header and details pane) — never the five raw rate-class token counts
- it should still expose the five raw rate-class token counts via `GET /api/hera`, unaffected by this rendering choice

## Discovery findings

- `scanContextSize` (coord_hook.go:719-764) is a last-value snapshot (overwrites `size` each line), not a running sum — accumulating token COUNTS needs a NEW summing scan, not converting the existing overwrite into an addition; but the DOLLAR figure needs a further, different mechanism still (see below).
- `task_meta` rows are deleted on task archive (`DeleteMetaForTask` via `SetArchived(archived=true)`, tasks.go:293 + task_meta.go:161) — this is why token totals belong on `hera_bindings`, not `task_meta`, despite `context_size`'s precedent living there.
- **Live-repricing-at-display-time is factually wrong for historical cost** (found via Aaron's review, hera message #4764): a rate-table change must never retroactively alter an already-recorded dollar figure. This forced Decision 2 from "store raw counts, compute $ live at read time" to "store raw counts AND accrue a persisted $ total incrementally, priced at the moment each delta is observed."
- **The transcript DOES carry an exact per-TTL cache-write breakdown** (found via direct inspection of this session's own transcript file, not guessed): `usage.cache_creation.ephemeral_1h_input_tokens` / `ephemeral_5m_input_tokens`, confirmed to sum exactly to the flat `cache_creation_input_tokens` `scanContextSize` already reads. This eliminated the need for a blended cache-write rate entirely (Decision 3), and eliminated the need to infer TTL from auth-mode context even though that inference is independently deterministic (coordinator's research into Claude Code's own TTL-selection behavior).
- `agent.KnownModels` (agent.go:213-227) returns CLI aliases (`"opus"/"sonnet"/"haiku"/"fable"`, `"gpt-5-codex"/"gpt-5"`), not versioned model IDs — the rate table's keys and its alias-drift risk both follow from this.
- `RoleView.AppliedModel` / `agent.ResolveModel` (hera_tiering.go:96-97, agent.go:129) already solve "what model does this role run," live, per role — reused as the rate-table lookup key at accrual time, server-side, rather than building a new resolution path.
- `Model.SubtreeAgentCount` / `BridgeSubtree` (model.go:811-821) already solve double-count-safe subtree aggregation for a different metric (agent count) — the cost rollup reuses the walk, diverging only in which role kinds it sums and in bypassing the nuked-role exclusion; because cost is now pre-priced at accrual time, the rollup itself is pure addition, simpler than originally drafted.
- `internal/llm/namegen.go`'s ~$0.0034/call figure is a one-off comment justifying a timeout constant, not a reusable cost module — there is no existing pricing infrastructure to build on.
- The existing "Orchestrator and role row rendering (area 3)" hera-view requirement's agent-count badge is the natural home for a subtree cost figure in the TUI (not the width-constrained rail row, which is already fully committed to the context-pressure indicator's reserved 2-column slot).
- **`internal/profiles/` already ships the exact seed/install/precedence shape `rates.toml` needs** (found while resolving Open Question 2): `seeds.go`'s `//go:embed seeds/*.toml` + `SeedNames`, `install.go`'s `InstallDefaults` (skip-if-exists, never overwrite, explicit-invocation-only), and `load.go`'s `Loader.locate` (RepoDir checked before LibraryDir). Confirmed `Loader.Load` has NO caching layer at all (`grep` for `[Cc]ache` in that package returns nothing) — every lookup re-reads the TOML file from disk, which is exactly why reusing this shape needs no live-reload mechanism for rates, unlike `config.toml`'s override layer elsewhere in the project.
- **`InstallDefaults` itself is only ever invoked explicitly today**, from a Settings UI action (`internal/tui/app.go:1508`), never automatically at daemon startup — a deliberate divergence point this design calls out rather than silently mirroring, since rates (unlike opt-in diligence profiles) must be present for an always-on background mechanism to function at all.
