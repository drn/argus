## Why

Argus tracks per-role token telemetry as a live, overwritten snapshot (`task_meta(hera, context_size)`), not a history — there is no way to know how much a hera coordinator's subtree has actually cost to run. The only prior "cost" reference in the codebase is a one-off comment justifying an unrelated timeout constant. Aaron and the coordinator want a deterministic, per-coordinator estimated dollar cost, computed from real token usage already visible in the Claude Code transcripts `argus coord-hook` already parses once per turn — a small, targeted extension of existing instrumentation rather than new upstream tracking, and materially more accurate than a duration × hourly-rate proxy.

## What Changes

- Extend the `argus coord-hook` `Stop`-hook's transcript scan to additionally SUM (not snapshot) `input_tokens`/`cache_creation_input_tokens`/`cache_read_input_tokens`/`output_tokens` across every turn, and persist the running per-rate-class totals.
- Add four new columns to `hera_bindings` (`tokens_input`, `tokens_cache_write`, `tokens_cache_read`, `tokens_output`) as the persistence target — deliberately NOT `task_meta`, because `task_meta` rows are deleted when the underlying task is archived, which would silently drop recorded spend on a routine lifecycle event.
- Add a new hera-scoped REST write path for the hook to stamp a live binding's token totals, distinct from the existing generic `task_meta` write endpoint.
- Add a deterministic $/token rate table (embedded Go default + `config.toml` override), keyed by the same model-alias strings `agent.KnownModels`/`RoleView.AppliedModel` already use, with distinct rates for fresh-input/cache-write/cache-read/output tokens.
- Add a per-role cost computation (sum of a role's token totals across all its bindings, priced at read time against the current rate table) and a per-coordinator/orchestrator subtree-cost rollup that mirrors `Model.SubtreeAgentCount`'s existing double-count-safe subtree walk, but sums every role kind and deliberately includes nuked children's recorded spend (a new, narrow DB query path — the existing `ListHeraRoles` nuked-exclusion behavior is left untouched for every other caller).
- Surface the resulting cost figures on `GET /api/hera` (new fields), the TUI's Hera orchestrator header, the web SPA's Hera tab, and the macOS app's Hera tab — display-only, no new mutation surface on any client.
- No retroactive cost for bindings that predate this change; a binding with no recorded usage shows "n/a", not "$0.00".

## Capabilities

### New Capabilities

- `cost-estimation`: the token-sum hook extension, `hera_bindings` persistence, rate table, and per-role/per-coordinator cost rollup computation.

### Modified Capabilities

- `rest-api`: `GET /api/hera`'s `heraRoleJSON`/`heraOrchJSON` response shape gains per-role token/cost fields and a per-orchestrator subtree-cost field.
- `hera-view`: the TUI's orchestrator header row (the existing agent-count badge requirement) gains a subtree-cost display.
- `mobile-pwa`: the web SPA's Hera tab role rows render the new REST cost fields, read-only.
- `macos-app`: the macOS app's Hera tab role rows render the new REST cost fields, read-only.

## Impact

- **Code:** `cmd/argus/coord_hook.go` (new token-sum scan function alongside `scanContextSize`), `internal/db/schema.go` + a new `internal/db` accessor (new `hera_bindings` columns and their read/write helpers), a new `internal/db` cost-rollup query (nuked-inclusive), a new `internal/cost` (or similar) package for the rate table and USD computation, `internal/api/hera.go` (new REST write endpoint + extended roster DTO fields), `internal/tui/hera/` (`model.go`, `rail.go` — subtree-cost display), `internal/api/static/` (SPA Hera tab rendering), `macos/Sources/ArgusKit/Models+Hera.swift` and the SwiftUI Hera tab view.
- **Data:** additive `hera_bindings` schema columns (idempotent, self-evolving, no migration script, no backfill).
- **APIs:** one new REST endpoint (hook-facing, not user-facing); `GET /api/hera`'s response shape grows new fields (additive, non-breaking for existing consumers).
- **No changes** to `ListHeraRoles`'s existing signature/behavior, to `task_meta`/`context_size`'s existing storage or budget/nudge logic, or to any existing mutation surface.
