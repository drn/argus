**Design doc:** `openspec/changes/add-coordinator-cost-estimate/design.md`

**Status:** Implemented (hera message #4774 — Aaron's explicit go-ahead). All stages complete; `make pre-pr` passes (build/vet/fmt-check/lint-pr clean; `vuln` shows only pre-existing stdlib-toolchain CVEs, advisory/continue-on-error in CI; `test-cover-gate` passes at 88.8% ≥ 88% floor). Two scope notes surfaced during implementation and reflected in the specs: REST's `subtree_cost_usd` and the Details-pane "Cost" field are non-recursive (this-orchestrator-only) — the TUI rail's `Model.SubtreeCostUSD` is the only full recursive rollup, for the reason documented in `design.md` Decision 5 and `gotchas/hera-view.md`.

**Scope note:** this change is TUI-only (Decision 5) and renders the blended total only (Decision 7). Web SPA and macOS Hera-tab rendering are an explicit named follow-up, NOT a stage below — see `design.md`'s Non-Goals.

## 1. Tests

- [x] 1.1 Write failing tests for the Stop-hook raw-token scan (sidechain exclusion, idempotent resum, all-role-kind accumulation, output-token capture, TTL-split cache-write fields, missing-breakdown fallback to 5-minute tier) from `specs/cost-estimation/spec.md`
- [x] 1.2 Write failing tests for the `rates.toml` seed/install/precedence mechanism (never-overwrite-existing, in-repo-takes-precedence, no-restart-needed-on-hand-edit)
- [x] 1.3 Write failing tests for `hera_bindings` token-column and `cost_usd_accrued` persistence and archive-independence (archiving a task leaves both unchanged; a recycled role's total spans every incarnation)
- [x] 1.4 Write failing tests for accrual-time stamping specifically (a rate-table change affects only future deltas, never already-accrued cost; a duplicate hook invocation with a zero delta adds nothing; a rate added later never retroactively prices earlier unrated usage)
- [x] 1.5 Write failing tests for the subtree cost rollup (pure addition of already-priced `cost_usd_accrued`, nuked-inclusion, exactly-once nested-sub-coordinator counting, `ListHeraRoles`'s other callers unaffected)
- [x] 1.6 Write failing tests for the extended `GET /api/hera` DTO fields (populated vs. omitted-when-unmeasured, on both role and orchestrator envelopes)
- [x] 1.7 Confirm every acceptance criterion in `design.md` maps to a failing test written above (Prove-It Pattern) before starting Stage 2

## 2. Rate table (rates.toml seed/install/precedence)

**Depends on:** Stage 1

- [x] 2.1 Author a committed `rates.toml` seed file (e.g. `internal/pricing/rates.toml`) with five per-model rate classes (input / cache-write-1h / cache-write-5m / cache-read / output), covering at minimum the Claude and Codex aliases `agent.KnownModels` curates
- [x] 2.2 `//go:embed` the seed, mirroring `internal/profiles/seeds.go`'s `seedFS` shape (one file, not a named set)
- [x] 2.3 Add an install function mirroring `profiles.InstallDefaults` (`internal/profiles/install.go:15-35`): write the embedded seed to `~/.argus/rates.toml` only if absent, never overwrite. Unlike `InstallDefaults`'s explicit-invocation-only contract, wire this to run automatically and idempotently (e.g. at daemon startup)
- [x] 2.4 Add a lookup function mirroring `profiles.Loader.locate` (`internal/profiles/load.go:33-47`): check an in-repo `rates.toml` before the installed library copy; read fresh from disk on every call, no caching
- [x] 2.5 Add a `PriceDelta(tokenDeltas, rate) -> costDelta` helper for pricing a single accrual increment — NOT a whole-total pricing function; a model with no rate entry yields "not priced," never zero
- [x] 2.6 Unit tests: never-overwrite, in-repo-precedence, no-caching (hand-edit visible on next call), per-class pricing correctness, no-rate-entry-yields-unpriced

## 3. Hook token-accumulation, binding persistence, and accrual-time cost stamping

**Depends on:** Stage 1, Stage 2

- [x] 3.1 Add `tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output` INTEGER columns (default 0) AND `cost_usd_accrued` REAL column (default 0) to `hera_bindings` in `internal/db/schema.go`, following the existing idempotent CREATE-TABLE-carries-column-inline pattern
- [x] 3.2 Add a DB accessor to read a binding's current five raw totals and `cost_usd_accrued`, and a second accessor to write new values for all six columns together (single atomic update)
- [x] 3.3 Add a per-role aggregation accessor summing `cost_usd_accrued` (pure addition, no repricing) across `ListHeraBindingsByRole(roleID)` (live and ended bindings)
- [x] 3.4 Add a new scan function in `cmd/argus/coord_hook.go` (alongside `scanContextSize`) that sums `input_tokens` / `cache_read_input_tokens` / `output_tokens` / `usage.cache_creation.ephemeral_1h_input_tokens` / `usage.cache_creation.ephemeral_5m_input_tokens` across every non-sidechain main-chain assistant message, falling back to the 5-minute bucket when the nested `cache_creation` object is absent
- [x] 3.5 Add a new hera-scoped REST write endpoint (e.g. `PUT /api/hera/bindings/current/tokens`) that resolves the caller's `ARGUS_TASK_ID` to its currently-live binding. The handler SHALL: read the binding's previous five raw totals + `cost_usd_accrued`; accept the freshly-resummed five raw totals in the request; compute the per-class delta; price the delta against Stage 2's rate table using the role's live-resolved `AppliedModel`; add the priced delta to `cost_usd_accrued`; write the new raw totals + new `cost_usd_accrued` together
- [x] 3.6 Wire the Stop-hook subcommand to call the new endpoint with the freshly-recomputed raw totals on every invocation that also stamps `context_size`, for every hera-bound role kind (not gated on coordinator-only, unlike the budget/nudge logic)
- [x] 3.7 Tests: a duplicate/retried invocation with an unchanged transcript adds zero to `cost_usd_accrued`; archiving the underlying task leaves all six columns unchanged

## 4. Subtree cost rollup

**Depends on:** Stage 3

- [x] 4.1 Add a new, dedicated nuked-inclusive role-listing query path (e.g. `ListHeraRolesForCostRollup`) alongside the existing `ListHeraRoles`, which stays unmodified for every other caller
- [x] 4.2 Add a subtree-cost rollup mirroring `Model.SubtreeAgentCount`'s `BridgeSubtree` traversal, summing every role kind's (including coordinators) already-priced `cost_usd_accrued` — pure addition, no rate-table lookup at this layer — and counting a nested sub-coordinator's cost exactly once via the same bridging-row convention
- [x] 4.3 Tests: nuked child's cost included in the subtree total; nested sub-coordinator counted exactly once; `ListHeraRoles` and its other existing callers (agent count, needs-input rollup) demonstrably unaffected

## 5. REST DTO

**Depends on:** Stage 4

- [x] 5.1 Extend `heraRoleJSON` (`internal/api/hera.go`) with `tokens_input`/`tokens_cache_write_1h`/`tokens_cache_write_5m`/`tokens_cache_read`/`tokens_output`/`cost_usd` (the last sourced directly from persisted `cost_usd_accrued`, no computation in this handler)
- [x] 5.2 Extend `heraOrchJSON` with `subtree_cost_usd` (sourced from Stage 4's rollup)
- [x] 5.3 Wire `handleHera` to populate the new fields, omitting (not zeroing) unmeasured/uncurated cases
- [x] 5.4 Tests: populated-when-measured, omitted-when-unmeasured, on both role and orchestrator envelopes

## 6. TUI render (blended total only)

**Depends on:** Stage 5

- [x] 6.1 Extend `RoleView` (`internal/tui/hera/model.go`) with the per-role blended `cost_usd` field only (no per-rate-class fields — Decision 7), and the Model with a subtree-cost accessor
- [x] 6.2 Render the subtree cost figure alongside the existing agent-count badge in the orchestrator header (`drawOrchRow`, `internal/tui/hera/rail.go`), omitted when nothing in the subtree is measured
- [x] 6.3 Render the per-role blended cost figure in the details pane (not the width-constrained rail row) — no raw token/rate-class breakdown
- [x] 6.4 Smoke test asserting the header renders the cost figure when present and omits it when absent

## 7. Documentation and gotchas

**Depends on:** Stages 2-6

- [x] 7.1 Document the token-sum-vs-snapshot distinction, the `task_meta`-archive-deletion rationale for choosing `hera_bindings`, the accrual-time-stamping-vs-read-time-repricing correction, and the `rates.toml` seed/install pattern's deliberate divergence from the profiles precedent's manual-only trigger (cite hera messages #4764/#4770) in `context/knowledge/gotchas/` (per CLAUDE.md's non-obvious-gotcha documentation rule)
- [x] 7.2 Add `uxlog` calls for the new REST write path and rollup failures (state transitions, silently-skipped work) per CLAUDE.md's Logging Requirements
- [x] 7.3 Update the README Reference appendix's REST endpoint table for the new `PUT /api/hera/bindings/current/tokens` endpoint and the extended `GET /api/hera` fields
- [x] 7.4 Run `make pre-pr`; confirm coverage floor on touched packages
