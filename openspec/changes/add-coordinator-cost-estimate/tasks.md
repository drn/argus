**Design doc:** `openspec/changes/add-coordinator-cost-estimate/design.md`

**Status:** Proposal only. Do NOT execute this plan until Aaron has approved the design and answered the Open Questions in `design.md`. No implementation has been started.

## 1. Tests

- [ ] 1.1 Write failing tests for the Stop-hook raw-token scan (sidechain exclusion, idempotent resum, all-role-kind accumulation, output-token capture, TTL-split cache-write fields, missing-breakdown fallback to 5-minute tier) from `specs/cost-estimation/spec.md`
- [ ] 1.2 Write failing tests for `hera_bindings` token-column and `cost_usd_accrued` persistence and archive-independence (archiving a task leaves both unchanged; a recycled role's total spans every incarnation)
- [ ] 1.3 Write failing tests for the five-class rate table (curated-model lookup, uncurated-model no-figure) and for accrual-time stamping specifically (a rate-table change affects only future deltas, never already-accrued cost; a duplicate hook invocation with a zero delta adds nothing; a rate added later never retroactively prices earlier unrated usage)
- [ ] 1.4 Write failing tests for the subtree cost rollup (pure addition of already-priced `cost_usd_accrued`, nuked-inclusion, exactly-once nested-sub-coordinator counting, `ListHeraRoles`'s other callers unaffected)
- [ ] 1.5 Write failing tests for the extended `GET /api/hera` DTO fields (populated vs. omitted-when-unmeasured, on both role and orchestrator envelopes)
- [ ] 1.6 Confirm every acceptance criterion in `design.md` maps to a failing test written above (Prove-It Pattern) before starting Stage 2

## 2. Rate table

**Depends on:** Stage 1

- [ ] 2.1 Add a new package (e.g. `internal/cost`) defining a per-model rate struct with FIVE classes (input / cache-write-1h / cache-write-5m / cache-read / output, $ per token) and an embedded default map keyed by `agent.KnownModels`'s alias strings
- [ ] 2.2 Resolve Open Question 2 (embedded-only vs `config.toml` override) with Aaron before implementing; if override is chosen, add `config.toml` loading following the project's existing config-override-layer precedence and mtime live-reload pattern
- [ ] 2.3 Add a `PriceDelta(tokenDeltas, rate) -> costDelta` helper for pricing a single accrual increment — NOT a whole-total pricing function; a model with no rate entry yields "not priced," never zero
- [ ] 2.4 Unit tests: per-class pricing correctness, no-rate-entry-yields-unpriced

## 3. Hook token-accumulation, binding persistence, and accrual-time cost stamping

**Depends on:** Stage 1, Stage 2

- [ ] 3.1 Add `tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output` INTEGER columns (default 0) AND `cost_usd_accrued` REAL column (default 0) to `hera_bindings` in `internal/db/schema.go`, following the existing idempotent CREATE-TABLE-carries-column-inline pattern
- [ ] 3.2 Add a DB accessor to read a binding's current five raw totals and `cost_usd_accrued`, and a second accessor to write new values for all six columns together (single atomic update)
- [ ] 3.3 Add a per-role aggregation accessor summing `cost_usd_accrued` (pure addition, no repricing) across `ListHeraBindingsByRole(roleID)` (live and ended bindings)
- [ ] 3.4 Add a new scan function in `cmd/argus/coord_hook.go` (alongside `scanContextSize`) that sums `input_tokens` / `cache_read_input_tokens` / `output_tokens` / `usage.cache_creation.ephemeral_1h_input_tokens` / `usage.cache_creation.ephemeral_5m_input_tokens` across every non-sidechain main-chain assistant message, falling back to the 5-minute bucket when the nested `cache_creation` object is absent
- [ ] 3.5 Add a new hera-scoped REST write endpoint (e.g. `PUT /api/hera/bindings/current/tokens`) that resolves the caller's `ARGUS_TASK_ID` to its currently-live binding. The handler SHALL: read the binding's previous five raw totals + `cost_usd_accrued`; accept the freshly-resummed five raw totals in the request; compute the per-class delta; price the delta against Stage 2's rate table using the role's live-resolved `AppliedModel`; add the priced delta to `cost_usd_accrued`; write the new raw totals + new `cost_usd_accrued` together
- [ ] 3.6 Wire the Stop-hook subcommand to call the new endpoint with the freshly-recomputed raw totals on every invocation that also stamps `context_size`, for every hera-bound role kind (not gated on coordinator-only, unlike the budget/nudge logic)
- [ ] 3.7 Tests: a duplicate/retried invocation with an unchanged transcript adds zero to `cost_usd_accrued`; archiving the underlying task leaves all six columns unchanged

## 4. Subtree cost rollup

**Depends on:** Stage 3

- [ ] 4.1 Add a new, dedicated nuked-inclusive role-listing query path (e.g. `ListHeraRolesForCostRollup`) alongside the existing `ListHeraRoles`, which stays unmodified for every other caller
- [ ] 4.2 Add a subtree-cost rollup mirroring `Model.SubtreeAgentCount`'s `BridgeSubtree` traversal, summing every role kind's (including coordinators) already-priced `cost_usd_accrued` — pure addition, no rate-table lookup at this layer — and counting a nested sub-coordinator's cost exactly once via the same bridging-row convention
- [ ] 4.3 Tests: nuked child's cost included in the subtree total; nested sub-coordinator counted exactly once; `ListHeraRoles` and its other existing callers (agent count, needs-input rollup) demonstrably unaffected

## 5. REST DTO

**Depends on:** Stage 4

- [ ] 5.1 Extend `heraRoleJSON` (`internal/api/hera.go`) with `tokens_input`/`tokens_cache_write_1h`/`tokens_cache_write_5m`/`tokens_cache_read`/`tokens_output`/`cost_usd` (the last sourced directly from persisted `cost_usd_accrued`, no computation in this handler)
- [ ] 5.2 Extend `heraOrchJSON` with `subtree_cost_usd` (sourced from Stage 4's rollup)
- [ ] 5.3 Wire `handleHera` to populate the new fields, omitting (not zeroing) unmeasured/uncurated cases
- [ ] 5.4 Tests: populated-when-measured, omitted-when-unmeasured, on both role and orchestrator envelopes

## 6. TUI render

**Depends on:** Stage 5

- [ ] 6.1 Extend `RoleView` (`internal/tui/hera/model.go`) with per-role cost/token fields, and the Model with a subtree-cost accessor
- [ ] 6.2 Render the subtree cost figure alongside the existing agent-count badge in the orchestrator header (`drawOrchRow`, `internal/tui/hera/rail.go`), omitted when nothing in the subtree is measured
- [ ] 6.3 Render per-role cost/token breakdown in the details pane (not the width-constrained rail row)
- [ ] 6.4 Smoke test asserting the header renders the cost figure when present and omits it when absent

## 7. Web SPA render

**Depends on:** Stage 5

- [ ] 7.1 Render `subtree_cost_usd` on orchestrator rows and `cost_usd` on role rows in the Hera tab (`internal/api/static/`)
- [ ] 7.2 HTML-escape as required by the existing Hera tab requirement; omit cleanly (no `$0.00`) when the field is absent
- [ ] 7.3 Bump `SW_VERSION` in `internal/api/static/sw.js` per the shell-asset-change rule

## 8. macOS render

**Depends on:** Stage 5

- [ ] 8.1 Extend `HeraRole`/`HeraOrchestrator` (`macos/Sources/ArgusKit/Models+Hera.swift`) with the matching decoded cost/token fields
- [ ] 8.2 Render the figures in the SwiftUI Hera tab's role-row view, omitted cleanly when absent
- [ ] 8.3 `make mac-test` coverage for the new decode fields and row rendering

## 9. Documentation and gotchas

**Depends on:** Stages 3-8

- [ ] 9.1 Document the token-sum-vs-snapshot distinction, the `task_meta`-archive-deletion rationale for choosing `hera_bindings`, and the accrual-time-stamping-vs-read-time-repricing correction (why it matters, cite hera message #4764) in `context/knowledge/gotchas/` (per CLAUDE.md's non-obvious-gotcha documentation rule)
- [ ] 9.2 Add `uxlog` calls for the new REST write path and rollup failures (state transitions, silently-skipped work) per CLAUDE.md's Logging Requirements
- [ ] 9.3 Update the README Reference appendix's REST endpoint table for the new `PUT /api/hera/bindings/current/tokens` endpoint and the extended `GET /api/hera` fields
- [ ] 9.4 Run `make pre-pr` and `make mac-test`; confirm coverage floor on touched packages
