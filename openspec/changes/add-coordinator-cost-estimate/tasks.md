**Design doc:** `openspec/changes/add-coordinator-cost-estimate/design.md`

**Status:** Proposal only. Do NOT execute this plan until Aaron has approved the design and answered the Open Questions in `design.md`. No implementation has been started.

## 1. Tests

- [ ] 1.1 Write failing tests for the Stop-hook token-sum scan (sidechain exclusion, idempotent resum, all-role-kind accumulation, output-token capture) from `specs/cost-estimation/spec.md`
- [ ] 1.2 Write failing tests for `hera_bindings` token-column persistence and archive-independence (archiving a task leaves its binding's totals unchanged; a recycled role's total spans every incarnation)
- [ ] 1.3 Write failing tests for the rate table (curated-model lookup, uncurated-model no-figure, config-override precedence, rate-update repricing-without-rescan)
- [ ] 1.4 Write failing tests for the subtree cost rollup (nuked-inclusion, exactly-once nested-sub-coordinator counting, `ListHeraRoles`'s other callers unaffected)
- [ ] 1.5 Write failing tests for the extended `GET /api/hera` DTO fields (populated vs. omitted-when-unmeasured, on both role and orchestrator envelopes)
- [ ] 1.6 Confirm every acceptance criterion in `design.md` maps to a failing test written above (Prove-It Pattern) before starting Stage 2

## 2. Hook token-accumulation and binding persistence

**Depends on:** Stage 1

- [ ] 2.1 Add `tokens_input`, `tokens_cache_write`, `tokens_cache_read`, `tokens_output` INTEGER columns (default 0) to `hera_bindings` in `internal/db/schema.go`, following the existing idempotent CREATE-TABLE-carries-column-inline pattern
- [ ] 2.2 Add a DB accessor to update a binding's four token columns by binding id, and extend the `HeraBinding` struct/scan helpers to read them
- [ ] 2.3 Add a per-role aggregation accessor summing the four columns across `ListHeraBindingsByRole(roleID)` (live and ended bindings)
- [ ] 2.4 Add a new scan function in `cmd/argus/coord_hook.go` (alongside `scanContextSize`) that sums `input_tokens`/`cache_creation_input_tokens`/`cache_read_input_tokens`/`output_tokens` across every non-sidechain main-chain assistant message
- [ ] 2.5 Add a new hera-scoped REST write endpoint (e.g. `PUT /api/hera/bindings/current/tokens`) that resolves the caller's `ARGUS_TASK_ID` to its currently-live binding and overwrites its four token columns
- [ ] 2.6 Wire the Stop-hook subcommand to call the new endpoint with the freshly-recomputed totals on every invocation that also stamps `context_size`, for every hera-bound role kind (not gated on coordinator-only, unlike the budget/nudge logic)

## 3. Rate table and USD computation

**Depends on:** Stage 1 (independent of Stage 2 — can run in parallel)

- [ ] 3.1 Add a new package (e.g. `internal/cost`) defining a per-model rate struct (input/cache-write/cache-read/output $ per token) and an embedded default map keyed by `agent.KnownModels`'s alias strings
- [ ] 3.2 Add `config.toml` override loading for per-model rate entries, following the project's existing config-override-layer precedence (override wins over embedded default) and mtime live-reload pattern
- [ ] 3.3 Add a `ComputeCostUSD(tokens, rate)` helper; a model with no rate entry (override or default) yields "not computed," never zero
- [ ] 3.4 Unit tests: override-takes-precedence, and a rate-table change reprices previously-recorded token totals with no re-scan

## 4. Subtree cost rollup

**Depends on:** Stage 2, Stage 3

- [ ] 4.1 Add a new, dedicated nuked-inclusive role-listing query path (e.g. `ListHeraRolesForCostRollup`) alongside the existing `ListHeraRoles`, which stays unmodified for every other caller
- [ ] 4.2 Add per-role cost computation joining a role's aggregated token totals (2.3) against the rate table (3.3), keyed by that role's live-resolved `AppliedModel`
- [ ] 4.3 Add a subtree-cost rollup mirroring `Model.SubtreeAgentCount`'s `BridgeSubtree` traversal, summing every role kind (including coordinators) and counting a nested sub-coordinator's cost exactly once via the same bridging-row convention
- [ ] 4.4 Tests: nuked child's cost included in the subtree total; nested sub-coordinator counted exactly once; `ListHeraRoles` and its other existing callers (agent count, needs-input rollup) demonstrably unaffected

## 5. REST DTO

**Depends on:** Stage 4

- [ ] 5.1 Extend `heraRoleJSON` (`internal/api/hera.go`) with `tokens_input`/`tokens_cache_write`/`tokens_cache_read`/`tokens_output`/`cost_usd`
- [ ] 5.2 Extend `heraOrchJSON` with `subtree_cost_usd`
- [ ] 5.3 Wire `handleHera` to populate the new fields from Stage 4's rollup, omitting (not zeroing) unmeasured/uncurated cases
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

**Depends on:** Stages 2-8

- [ ] 9.1 Document the token-sum-vs-snapshot distinction and the `task_meta`-archive-deletion rationale for choosing `hera_bindings` in `context/knowledge/gotchas/` (per CLAUDE.md's non-obvious-gotcha documentation rule)
- [ ] 9.2 Add `uxlog` calls for the new REST write path and rollup failures (state transitions, silently-skipped work) per CLAUDE.md's Logging Requirements
- [ ] 9.3 Update the README Reference appendix's REST endpoint table for the new `PUT /api/hera/bindings/current/tokens` endpoint and the extended `GET /api/hera` fields
- [ ] 9.4 Run `make pre-pr` and `make mac-test`; confirm coverage floor on touched packages
