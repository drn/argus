## Context

Argus records per-role token telemetry as a live snapshot, not a history: the `argus coord-hook` `Stop`-hook subcommand (`cmd/argus/coord_hook.go:719`, `scanContextSize`) parses the Claude Code transcript JSONL once per turn and OVERWRITES a single `task_meta(hera, context_size)` value with the LATEST main-chain assistant message's `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` (coord_hook.go:742-758). Output tokens are never read. There is no running total, no per-turn history, and no cost concept anywhere in the codebase — the only prior "cost" reference is a one-off comment in `internal/llm/namegen.go:7,132` justifying a timeout constant (~$0.0034/call at Haiku 4.5 pricing), not a reusable pattern.

Duration is already solid (`tasks.created_at/started_at/ended_at`, `hera_bindings.started_at/ended_at/end_reason`), and the rollup path already exists for other per-subtree metrics: `Model.SubtreeAgentCount` (`internal/tui/hera/model.go:811`) walks `Model.BridgeSubtree(orchID)` to sum a metric across a coordinator's whole nested subtree without double-counting a nested sub-coordinator's agent (counted once, via the bridging worker row). This design reuses that exact walk for cost.

Model/backend per role is resolved live, not stored on `hera_roles` (which has no model column): `internal/tui/hera_tiering.go:31-98` (`resolveHeraTier`) calls `agent.ResolveModel` (`internal/agent/agent.go:129`) to fill `RoleView.AppliedModel`. `agent.KnownModels` (agent.go:213-227) returns CLI ALIASES, not versioned model IDs — `"opus"/"sonnet"/"haiku"/"fable"` for Claude, `"gpt-5-codex"/"gpt-5"` for Codex; opencode/Pi/custom backends return `nil` (no curated list).

Three frontends read hera data today: the TUI native Hera view (`internal/tui/hera/`), the web SPA's Hera tab (`internal/api/static/`, fed by `GET /api/hera`), and the macOS app's read-only Hera tab (`macos/Sources/ArgusKit/Models+Hera.swift`, same endpoint). CLAUDE.md's Frontend Parity rule requires evaluating any user-facing/REST-exposed change against all three in the same PR, or an explicit named follow-up if deferred.

## Goals / Non-Goals

**Goals:**

- Deterministically estimate the dollar cost a hera coordinator's whole subtree (itself + every nested worker/sub-coordinator) has accrued, from real token usage already visible in Claude Code transcripts.
- Extend the existing per-turn transcript parse rather than add new instrumentation.
- Beat a duration × hourly-rate proxy on accuracy by pricing actual input/cache/output tokens per model.
- Surface the figure on all three frontends per the Frontend Parity rule (display-only; no new mutation surface).

**Non-Goals:**

- Retroactive cost for roles/bindings that ran before this ships (accrual starts only once this ships — see Decision 6).
- Exact accounting reconciled against Anthropic's actual invoice (this is an ESTIMATE — see Risks for the specific approximations).
- Cost for non-Claude backends beyond whatever `KnownModels` already curates (Codex's two aliases); opencode/Pi/custom models show no cost figure, not a fabricated one.
- Any new mutation surface (setting/editing rates from the UI, disputing a cost figure, budgets/alerts on cost) — this change is read-only telemetry, same posture as the existing REST hera roster.
- Rebuilding `scanContextSize`'s own retry-and-max-across-scans logic for the cost path — see Decision 1 for why a sum doesn't need it.

## Decisions

### Decision 1: Sum every turn's usage across the whole transcript on every hook call, not "stop overwriting and start adding"

`scanContextSize` (coord_hook.go:719-764) scans every non-sidechain `"assistant"` line and OVERWRITES a local `size` variable each time (line 758) — so the function's return value is the LATEST turn's total context, a high-water-mark snapshot, not a sum. This matters because it's tempting to misread the mission's "accumulate into a running total" as "just don't overwrite, add instead" — that would be wrong for context_size, whose whole point IS the latest snapshot (a context-window gauge).

Cost is different: Anthropic bills EVERY API call for its own full usage (fresh input + cache-write + cache-read + output), independent of what any other turn already paid for. The correct total-spend computation is the SUM of every turn's own usage, not just the latest. So this change adds a NEW scan function (e.g. `scanTokenTotals`) that keeps a running sum across all qualifying lines instead of overwriting, additionally reading `output_tokens` from the same `usage` object `scanContextSize` already parses (coord_hook.go:742-745) but never reads.

Each hook invocation re-scans the FULL transcript and OVERWRITES the persisted total with the fresh sum — it does NOT do `stored_total += this_turn's_tokens`. Rationale: a transcript is append-only, so a fresh full resum is idempotent (re-running it with an unchanged transcript reproduces the same total) and naturally monotonic (a later scan can only see the same or more complete lines, never fewer) — unlike an incremental `+=`, which double-counts if the Stop hook ever fires twice for the same turn (see Decision 1's alternative below). This also means the retry-and-take-max dance `readContextSizeReal`/`scanContextSize` need (to catch `transcript_path`'s async-write lag) is NOT needed here: an under-count from a transcript write that hasn't landed yet just self-corrects on the NEXT hook call, with no risk of ever over-counting or needing to judge freshness against a previous stamp.

### Decision 2: Persist raw per-rate-class token totals as new `hera_bindings` columns, NOT `task_meta`

The mission's suggested location was `task_meta` (mirroring `context_size`). Investigating that path surfaced a blocking problem: `DB.SetArchived(id, archived=true)` (`internal/db/tasks.go:293`) calls `DeleteMetaForTask` (`internal/db/task_meta.go:161-171`), which deletes EVERY `task_meta` row for that task. Archiving a task is a routine, expected lifecycle event for a finished hera worker — so storing token totals in `task_meta` would mean a coordinator's rollup total silently drops every archived child's recorded spend the moment it's archived. That defeats the entire point of a "deterministic" cost figure and would not be discovered until someone noticed a coordinator's total mysteriously shrinking.

`hera_bindings` (schema.go:546-554) has no such archive-triggered deletion and already holds the equivalent durable per-incarnation facts (`started_at`/`ended_at`/`end_reason`) for exactly this reason — a binding's history must survive the underlying task being archived. This design adds four columns to `hera_bindings`: `tokens_input`, `tokens_cache_write`, `tokens_cache_read`, `tokens_output` (INTEGER, default 0), via the schema's existing idempotent CREATE-TABLE-carries-column-inline pattern.

A live binding's totals are updated via a NEW hera-scoped REST write path (e.g. `PUT /api/hera/bindings/current/tokens`, resolved server-side from the caller's `ARGUS_TASK_ID` to its currently-live binding) rather than the generic `PUT /api/tasks/{id}/meta` endpoint the hook already uses for `context_size` — deliberately NOT reusing that endpoint, precisely to avoid the archive-coupled table. `context_size` itself is unaffected and keeps living in `task_meta` (it is legitimately ephemeral — nobody needs "current context %" once a task is archived and done).

USD is computed at DISPLAY/ROLLUP time by multiplying the stored raw token totals against the CURRENT rate table, not baked into storage as a dollar figure. This means correcting the rate table (Decision 3) immediately reprices every already-recorded binding's displayed cost with no backfill — a deliberately named property, not an accident.

A role can be re-bound/recycled (multiple incarnations over its life). Per-ROLE total = sum of the four columns across ALL of `ListHeraBindingsByRole(roleID)` (`internal/db/hera.go:1358-1364`, already returns every incarnation, live and ended, ordered by `started_at DESC`) — no new per-role storage needed.

**Alternative considered and rejected:** storing a single blended USD figure per binding instead of four raw token counts. Rejected because it forecloses re-pricing on a rate-table update and loses the input/cache-read transparency needed to reason about cache savings (see Decision 3's cache-read note).

### Decision 3: Rate table = embedded Go default seed + `config.toml` override, keyed by the same model-alias strings already in use

`agent.KnownModels` (agent.go:213-227) and `RoleView.AppliedModel` (fed by `agent.ResolveModel`, agent.go:129, already resolved live per role in `resolveHeraTier`, hera_tiering.go:96-97) are the existing, live source of "what model does this role run" — this design reuses that string AS the rate-table key, with no new model-resolution path. The table needs FOUR rates per model, not one blended $/token figure, because the transcript already distinguishes fresh input, cache-write, cache-read, and output tokens (coord_hook.go:742-745) and cache-read tokens are billed far cheaper than fresh input — collapsing them into one rate would misprice any role that benefits from prompt caching (which is most of them, since hera sessions are long-running and mostly re-read their own history).

A default table ships embedded in Go (e.g. `internal/cost`), versioned in source the same way `KnownModels` is. `config.toml` can override or add entries (e.g. `[cost.rates.<model>]` with `input`/`cache_write`/`cache_read`/`output` $/Mtok fields), mirroring the project's existing config-override-layer precedent (precedence + partial-map override + mtime live-reload, already used for backends/models) rather than inventing a new mechanism — so a pricing change from Anthropic can be corrected without a rebuild.

A model outside the curated set (opencode/custom/Pi backends, which return `nil` from `KnownModels`) has no rate entry and gets no cost figure — surfaced as "n/a", not a fabricated guess.

**Named risk, not solved here:** a CLI alias like `"sonnet"` always resolves to "whatever Anthropic currently designates," so if Anthropic rotates the underlying model version with different pricing, the alias-keyed rate silently goes stale until someone notices and corrects it via the config override. See Risks.

**Named approximation, not solved here:** Anthropic's cache-write pricing varies by cache TTL (5-minute vs 1-hour), but the transcript's `cache_creation_input_tokens` field (the one `scanContextSize` already parses) does not distinguish which TTL was used. The design uses one blended cache-write rate rather than a TTL-aware split. See Open Questions.

### Decision 4: Per-coordinator total mirrors `SubtreeAgentCount`'s double-count-safe walk, but sums every role kind and bypasses the `nuked_at` exclusion

`Model.SubtreeAgentCount` (`internal/tui/hera/model.go:811-821`) already solves "sum a metric across a coordinator's nested subtree without double-counting a nested sub-coordinator" by walking `m.BridgeSubtree(orchID)` and counting worker-kind roles only (deliberately excluding each subtree's own coordinator role, folded into its header, to avoid counting a nested sub-coordinator twice — once via its parent's bridging worker row, once via its own orchestrator's coordinator row). This design's `SubtreeCost` rollup reuses the same walk but sums ALL role kinds (coordinators spend real tokens too, unlike the agent-COUNT metric where a coordinator role isn't itself a countable "agent" slot) while preserving the exact same "count a nested sub-coordinator's spend exactly once, via its bridging representation" invariant `SubtreeAgentCount` already established.

The `nuked_at IS NULL` exclusion baked unconditionally into `ListHeraRoles` (`internal/db/hera.go:557-568`, specifically line 564, confirmed to apply even when `includeArchived=true`; the existing "Orchestrator and role row rendering" requirement in `hera-view` spec even documents today's agent-count badge as "a nuked role or orchestrator is never counted") is correct for DISPLAY (a nuked role is fully torn down and hidden everywhere) but wrong for a financial rollup — money genuinely spent by a since-nuked child must still count, or the total silently under-reports every time a coordinator nukes a finished child, which is normal cleanup. This design adds a NEW, separate DB query path for the cost rollup that DOES include nuked roles (e.g. `ListHeraRolesForCostRollup`, or an explicit `includeNuked` parameter threaded through a new function — NOT a change to `ListHeraRoles`'s existing signature/behavior, so every current caller is unaffected).

This is the one place this design touches existing DB query surface (additively) rather than only adding new fields/columns — flagged explicitly in Open Questions.

### Decision 5: Implement all three frontends (TUI, web SPA, macOS) in the same change, not a named-gap deferral

Per CLAUDE.md's Frontend Parity rule. Concretely:

- **REST** (`internal/api/hera.go`): extend `heraRoleJSON` (lines 15-27) with per-role `tokens_input`/`tokens_cache_write`/`tokens_cache_read`/`tokens_output`/`cost_usd` fields, and `heraOrchJSON` (lines 32-43) with a `subtree_cost_usd` field (the Decision-4 rollup). This is the single source both other clients read.
- **TUI**: the natural fit is NOT the rail's per-role row — that row is already width-squeezed (the existing "Worker/freelance rail rows show a context-pressure indicator" requirement reserves an exact 2-character trailing slot and truncates the role name to make room; a dollar figure needs more room than that). Instead, extend `Model.SubtreeAgentCount`'s sibling display — the orchestrator header's existing right-aligned bare-number agent-count badge (the "Orchestrator and role row rendering (area 3)" requirement, `internal/tui/hera/rail.go` `drawOrchRow`) — with the subtree cost total, and surface the per-role breakdown in the details pane (which already has room for a stacked roster view) rather than the rail row.
- **Web SPA** (`internal/api/static/`): the Hera tab's existing role-row rendering (per "Hera orchestration tab" in `mobile-pwa`) gets the new REST field rendered read-only, consistent with the existing "hera mutations are TUI-only, REST is read-only" standing gap already named in CLAUDE.md (a cost DISPLAY is a pure read, so it doesn't touch that gap).
- **macOS** (`macos/Sources/ArgusKit/Models+Hera.swift`): extend `HeraRole`/`HeraOrchestrator` (struct at line 5) with the matching decoded field(s) and render in the SwiftUI Hera tab's role-row view, per the existing "Hera roster (read-only)" requirement in `macos-app`.

Cost is display-only on every surface — no editing rates, no per-role cost mutation, matching the existing read-only REST hera posture.

### Decision 6: No retroactive cost; zero-across-all-four-columns is the "not yet measured" signal

A binding whose session ran (and possibly ended) before this ships never had its transcript re-scanned for token totals, so its four new columns default to and stay 0. Since every REAL session incurs nonzero token usage, "all four columns are exactly 0" is an unambiguous "never measured" signal at display time (rendered as "n/a", not "$0.00") — no separate NULL/sentinel column is needed. This is a deliberate simplification (YAGNI): it avoids nullable-column plumbing while remaining correct, because the one case it could get wrong (a real binding that is later found to have genuinely used zero tokens) cannot happen in practice.

## Risks / Trade-offs

- **[Risk]** Alias-keyed rate table (Decision 3) goes silently stale when Anthropic rotates which model version underlies a CLI alias (e.g. `"sonnet"`) at a different price. → **Mitigation:** the `config.toml` override lets the rate be corrected without a rebuild; document the alias-not-version caveat prominently in the shipped code/docs so a "wrong-looking" cost after a model swap isn't mistaken for a logic bug.
- **[Risk]** Custom/opencode/Pi-backend models have no curated identifier (`KnownModels` returns `nil`) and so no rate entry. → **Mitigation:** cost shows "n/a" for those roles rather than a fabricated guess; explicitly a Non-Goal to cover them.
- **[Risk]** Cache-write pricing varies by TTL (5-min vs 1-hour) but the parsed transcript field doesn't distinguish which was used (Decision 3). → **Mitigation:** documented approximation using one blended cache-write rate; flagged as an Open Question rather than solved.
- **[Risk]** The new cost-rollup DB query path deliberately includes nuked roles, diverging from every other existing hera rollup (agent count, needs-input rollup, etc.), which all exclude them (Decision 4). → **Mitigation:** implemented as a wholly separate function, not a change to `ListHeraRoles`'s existing behavior/callers; the divergence itself needs a documented rationale (this doc) so a future reader doesn't "fix" it back into consistency with the display rollups.
- **[Risk]** Full-transcript resum on every Stop-hook call grows linearly with conversation length. → **Mitigation:** none needed beyond the existing precedent — `scanContextSize`'s own doc comment (coord_hook.go:709-710) already accepts this cost class, reasoning the HTTP round-trip dominates; the cost path is actually CHEAPER than context_size's, since it needs no retry-and-max loop (Decision 1).
- **[Risk]** A new REST write path (`PUT /api/hera/bindings/current/tokens`) is additional daemon-side surface that will 405 on a stale-binary daemon until rebuilt+restarted (the existing `hera_send 405` class of issue). → **Mitigation:** none beyond the existing rebuild-and-restart playbook; noted so implementers don't chase a phantom bug.

## Migration Plan

New `hera_bindings` columns land via the schema's existing idempotent self-evolving pattern (CREATE-TABLE-carries-column-inline for fresh DBs; ALTER for existing ones) — no explicit migration script. No backfill for historical bindings (Decision 6) — additive only, consistent with the project's no-legacy-migration-code policy (one user, breaking changes are fine).

Suggested build order (detailed in `tasks.md`): (1) hook token-accumulation + `hera_bindings` columns + REST write path; (2) rate table + USD computation; (3) rollup queries (per-role, per-coordinator, including the nuked-inclusive carve-out); (4) REST DTO fields; (5) TUI render; (6) web SPA render; (7) macOS render.

## Open Questions

1. **Nuked-inclusive rollup query shape** (Decision 4): should this be a genuinely new DB function (e.g. `ListHeraRolesForCostRollup`), or should `ListHeraRoles` grow an `includeNuked` parameter alongside its existing `includeArchived`? Leaning toward a new function to leave `ListHeraRoles`'s contract and every existing caller untouched, but it's the one place this design proposes new DB query surface rather than purely new columns/fields — wanted a nod before implementation.
2. **Rate table home** (Decision 3): embedded-default-plus-`config.toml`-override (as designed), or embedded-only with no override (simpler, but a rebuild+redeploy is needed every time Anthropic changes pricing)? The override adds a config surface that needs documenting/discovering.
3. **Cache-write TTL granularity** (Decision 3): accept one blended cache-write rate for v1 (as designed), or is it worth checking whether Claude Code's transcript JSONL actually carries a TTL-scoped cache-creation breakdown that isn't being parsed today?
4. **UI parity scope** (Decision 5): implement all three surfaces in this same change as currently designed, or explicitly defer web SPA and/or macOS with a named follow-up (mirroring the existing "hera mutations are TUI-only" precedent) to ship the core mechanism faster? The Swift/macOS leg in particular is real added scope.
5. **Per-role token breakdown visibility**: expose the four raw rate-class token counts anywhere in the UI (for transparency/debugging — e.g. "1.2M cache-read + 40K fresh input + 8K output"), or only the final blended USD figure? Affects the REST DTO field count and how much detail the user sees per role vs. just the coordinator's bottom line.

## Acceptance criteria

**Decision 1 (token-sum scan):**

- it should sum `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens` + `output_tokens` across every non-sidechain main-chain assistant message in the transcript, not just the latest one
- it should exclude `isSidechain: true` lines from the sum, mirroring `scanContextSize`'s existing exclusion
- it should produce the same total when re-run twice against an unchanged transcript (idempotent)
- it should produce a total at least as large as any prior scan of a transcript that has only grown (monotonic, no regressions from partial reads)

**Decision 2 (persistence on hera_bindings, not task_meta):**

- it should persist the four running token totals as columns on the binding's `hera_bindings` row, keyed to the task's currently-live binding
- it should leave a binding's token totals unchanged when its underlying task is archived
- it should compute a role's total by summing its four columns across every `ListHeraBindingsByRole` row (live and ended)
- it should compute USD from stored raw token totals using the CURRENT rate table at read time, not a stored dollar amount

**Decision 3 (rate table):**

- it should look up rates by the same model-alias string already resolved into `RoleView.AppliedModel`
- it should apply distinct rates for fresh-input, cache-write, cache-read, and output tokens
- it should prefer a `config.toml`-provided rate over the embedded default when one is configured for that model
- it should surface no cost figure (not a zero, not a guess) for a model absent from both the config override and the embedded default table

**Decision 4 (subtree rollup):**

- it should sum a coordinator's own token totals together with every nested worker's and sub-coordinator's, walking the same `BridgeSubtree` traversal `SubtreeAgentCount` uses
- it should count a nested sub-coordinator's spend exactly once, not twice, via the same bridging-row convention `SubtreeAgentCount` already established
- it should include a nuked child role's recorded spend in its coordinator's subtree total
- it should exclude a nuked child role from every OTHER existing rollup's behavior (agent count, needs-input) unchanged — this design touches only the new cost-rollup query path

**Decision 5 (UI parity):**

- it should expose per-role token totals and cost, and per-orchestrator subtree cost, on `GET /api/hera`
- it should render the subtree cost total on the TUI orchestrator header alongside the existing agent-count badge
- it should render the REST cost fields, read-only, on the web SPA's Hera tab role rows
- it should render the REST cost fields, read-only, on the macOS app's Hera tab role rows
- it should expose no control on any surface that edits a rate, edits a recorded token total, or otherwise mutates cost data

**Decision 6 (no retroactive cost):**

- it should render "n/a" (not "$0.00") for a binding whose four token-total columns are all still 0
- it should apply no backfill or migration to bindings that predate this change

## Discovery findings

- `scanContextSize` (coord_hook.go:719-764) is a last-value snapshot (overwrites `size` each line), not a running sum — the mission's "accumulate into a running total" needs a NEW summing scan, not converting the existing overwrite into an addition.
- `task_meta` rows are deleted on task archive (`DeleteMetaForTask` via `SetArchived(archived=true)`, tasks.go:293 + task_meta.go:161) — this is why token totals belong on `hera_bindings`, not `task_meta`, despite `context_size`'s precedent living there.
- `agent.KnownModels` (agent.go:213-227) returns CLI aliases (`"opus"/"sonnet"/"haiku"/"fable"`, `"gpt-5-codex"/"gpt-5"`), not versioned model IDs — the rate table's keys and its alias-drift risk both follow from this.
- `RoleView.AppliedModel` / `agent.ResolveModel` (hera_tiering.go:96-97, agent.go:129) already solve "what model does this role run," live, per role — reused as the rate-table lookup key rather than building a new resolution path.
- `Model.SubtreeAgentCount` / `BridgeSubtree` (model.go:811-821) already solve double-count-safe subtree aggregation for a different metric (agent count) — the cost rollup reuses the walk, diverging only in which role kinds it sums and in bypassing the nuked-role exclusion.
- `internal/llm/namegen.go`'s ~$0.0034/call figure is a one-off comment justifying a timeout constant, not a reusable cost module — there is no existing pricing infrastructure to build on.
- The existing "Orchestrator and role row rendering (area 3)" hera-view requirement's agent-count badge is the natural home for a subtree cost figure in the TUI (not the width-constrained rail row, which is already fully committed to the context-pressure indicator's reserved 2-column slot).
