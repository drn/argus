## Why

Argus tracks per-role token telemetry as a live, overwritten snapshot (`task_meta(hera, context_size)`), not a history — there is no way to know how much a hera coordinator's subtree has actually cost to run. The only prior "cost" reference in the codebase is a one-off comment justifying an unrelated timeout constant. Aaron and the coordinator want a deterministic, per-coordinator estimated dollar cost, computed from real token usage already visible in the Claude Code transcripts `argus coord-hook` already parses once per turn — a small, targeted extension of existing instrumentation rather than new upstream tracking, and materially more accurate than a duration × hourly-rate proxy.

## What Changes

- Extend the `argus coord-hook` `Stop`-hook's transcript scan to additionally SUM (not snapshot) five token-usage classes across every turn — `input_tokens`, the TTL-split `cache_creation.ephemeral_1h_input_tokens`/`ephemeral_5m_input_tokens` (confirmed present in real transcripts by direct inspection, not guessed), `cache_read_input_tokens`, and `output_tokens` — and persist the running per-rate-class raw totals.
- Add five new raw-count columns plus a `cost_usd_accrued` dollar column to `hera_bindings` — deliberately NOT `task_meta`, because `task_meta` rows are deleted when the underlying task is archived, which would silently drop recorded spend on a routine lifecycle event.
- Add a new hera-scoped REST write path for the hook to stamp a live binding's raw totals; its daemon-side handler prices the DELTA since the previous stamp against the rate table AS IT STANDS AT THAT MOMENT and adds the result to `cost_usd_accrued` — accrual-time stamping, not a value recomputed live at read time. A later rate-table change therefore affects only future deltas; it can never retroactively shift an already-recorded historical cost.
- Add a deterministic five-class $/token rate table (fresh-input, cache-write-1h, cache-write-5m, cache-read, output), keyed by the same model-alias strings `agent.KnownModels`/`RoleView.AppliedModel` already use. Sourced from a committed `rates.toml` seed file mirroring the diligence-profiles seed/install/precedence pattern exactly (embed → install-to-`~/.argus/rates.toml`-if-absent, never overwrite, in-repo copy takes precedence) rather than an embedded-Go-map-plus-config-override — no live-reload mechanism is needed, since that pattern's loader already re-reads fresh from disk on every lookup.
- Add a per-role cost total (sum of a role's already-priced `cost_usd_accrued` across all its bindings — pure addition, no repricing) and a per-coordinator/orchestrator subtree-cost rollup mirroring `Model.SubtreeAgentCount`'s existing double-count-safe subtree walk, summing every role kind and deliberately including nuked children's recorded spend (a new, narrow DB query path — the existing `ListHeraRoles` nuked-exclusion behavior is left untouched for every other caller).
- Surface the resulting cost figures on `GET /api/hera` (new fields) and the TUI's Hera orchestrator header/details pane — blended total only, no raw per-rate-class breakdown rendered anywhere. **TUI-only for this change**: the web SPA and macOS Hera tabs render no cost UI here — an explicit, named follow-up (mirroring the existing "hera mutations are TUI-only" standing exception), not silence, per CLAUDE.md's Frontend Parity rule. The REST fields ship now regardless, so that follow-up needs no further backend work.
- Display-only, no new mutation surface on any client.
- No retroactive cost for bindings that predate this change, or for any accrual period whose model lacked a rate entry at the time; a binding with no recorded usage shows "n/a", not "$0.00".

## Capabilities

### New Capabilities

- `cost-estimation`: the token-sum hook extension, `hera_bindings` persistence, the `rates.toml` seed/install/precedence mechanism, accrual-time cost stamping, and per-role/per-coordinator cost rollup computation.

### Modified Capabilities

- `rest-api`: `GET /api/hera`'s `heraRoleJSON`/`heraOrchJSON` response shape gains per-role token/cost fields and a per-orchestrator subtree-cost field — populated regardless of which client renders them (native TUI in `--remote` mode reads through this same endpoint).
- `hera-view`: the TUI's orchestrator header row (the existing agent-count badge requirement) gains a blended subtree-cost display.

**Not modified in this change (explicit named follow-up, not silence):** `mobile-pwa` and `macos-app` — their Hera tabs render no cost UI yet. Tracked here as the required Frontend-Parity follow-up rather than a separate untracked gap.

## Impact

- **Code:** `cmd/argus/coord_hook.go` (new five-class token-sum scan alongside `scanContextSize`), `internal/db/schema.go` + new `internal/db` accessors (new `hera_bindings` columns, read-modify-write helpers for accrual, nuked-inclusive cost-rollup query), a new `internal/pricing` (or similar) package mirroring `internal/profiles`'s seed/install/load shape for `rates.toml`, `internal/api/hera.go` (new REST write endpoint + extended roster DTO fields), `internal/tui/hera/` (`model.go`, `rail.go` — subtree-cost display, blended total only).
- **Data:** additive `hera_bindings` schema columns (idempotent, self-evolving, no migration script, no backfill); a new committed `rates.toml` seed file, installed to `~/.argus/rates.toml` on first absence.
- **APIs:** one new REST endpoint (hook-facing, not user-facing); `GET /api/hera`'s response shape grows new fields (additive, non-breaking for existing consumers).
- **No changes** to `ListHeraRoles`'s existing signature/behavior, to `task_meta`/`context_size`'s existing storage or budget/nudge logic, to `profiles.InstallDefaults`/`Loader` themselves (mirrored, not modified), or to any existing mutation surface.
- **Explicitly out of scope for this change:** `internal/api/static/` (web SPA Hera tab rendering) and `macos/Sources/ArgusKit/` (macOS Hera tab rendering) — named follow-up, see Capabilities.
