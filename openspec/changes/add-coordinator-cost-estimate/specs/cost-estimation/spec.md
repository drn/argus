## ADDED Requirements

### Requirement: Stop-hook token-usage accumulation

The `argus coord-hook` Stop-hook subcommand SHALL, on every invocation that stamps `context_size` for any hera-bound role (coordinator, worker, or freelance alike), additionally scan `transcript_path` in full and compute four running totals — summed across EVERY main-chain (`type: "assistant"`, not `isSidechain`) message in the transcript — of `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, and `output_tokens`. Unlike `context_size` (which reflects only the LATEST such message's usage — a high-water-mark context-window gauge), these four totals SHALL be a SUM across every qualifying message, because Anthropic bills every API call independently for its own usage regardless of what a prior call already paid for.

The subcommand SHALL recompute all four totals afresh from the complete transcript on every invocation and OVERWRITE the persisted totals — it SHALL NOT add a delta to a previously stored value. This makes the computation idempotent against an unchanged transcript and immune to double-counting from a duplicate or retried Stop event. No bounded-retry-and-take-max logic (the mechanism `context_size` needs to judge a single scan's freshness against transcript_path's async-write lag) is required for these sums: an under-count from a transcript write that has not yet landed self-corrects on the very next invocation, with no risk of ever over-counting.

Derived from: `cmd/argus/coord_hook.go` (`scanContextSize`, the sibling function this accumulation extends alongside).

#### Scenario: Sidechain messages are excluded from the sum

- **WHEN** a transcript contains a line with `isSidechain: true` (a dispatched sub-agent's own turn)
- **THEN** that line's usage is excluded from all four running totals, mirroring `context_size`'s existing exclusion

#### Scenario: An unchanged transcript reproduces the same totals

- **WHEN** the hook fires twice with no new turns landing in the transcript between invocations
- **THEN** both invocations compute and persist identical values for all four totals

#### Scenario: Accumulation runs for every hera-bound role kind

- **WHEN** the hook fires in a session bound to a `worker`-kind or `freelance`-kind role
- **THEN** the four token totals are computed and persisted exactly as they would be for a `coordinator`-kind role, independent of the coordinator-only budget/nudge logic that gates on kind elsewhere

#### Scenario: Output tokens are captured

- **WHEN** a main-chain assistant message's usage includes a nonzero `output_tokens` value
- **THEN** that value contributes to the running output-token total, a field `context_size`'s own computation never reads

### Requirement: Token totals persist on the binding, independent of task archiving

The system SHALL persist the four running token totals as columns on the task's currently-live `hera_bindings` row (`tokens_input`, `tokens_cache_write`, `tokens_cache_read`, `tokens_output`), written via a hera-scoped REST endpoint distinct from the generic `task_meta` write path the hook uses for `context_size`. Archiving the underlying argus task SHALL NOT delete or reset a binding's token totals.

A role's total token usage SHALL be computed as the sum of its four columns across every one of that role's bindings — both the live binding (if any) and every prior ended binding — covering a role that has been re-bound or recycled over its lifetime.

Derived from: `internal/db/schema.go` (`hera_bindings`), `internal/db/hera.go` (`ListHeraBindingsByRole`), `internal/db/task_meta.go` (`DeleteMetaForTask`, the archive-coupled deletion this design deliberately avoids by not using `task_meta`).

#### Scenario: Archiving a task leaves its binding's token totals unchanged

- **WHEN** an argus task whose live-at-the-time hera binding carries nonzero token totals is archived
- **THEN** that binding's four token-total columns remain unchanged after archiving

#### Scenario: A recycled role's total spans every incarnation

- **WHEN** a role has been re-bound (ended one binding, started a new one) one or more times over its lifetime
- **THEN** the role's total token usage sums the four columns across every one of its bindings, live and ended alike

### Requirement: Deterministic per-model rate table

The system SHALL maintain a $/token rate table with four rate classes (fresh input, cache-write, cache-read, output) per model identifier, keyed by the same model-alias strings already produced by `agent.ResolveModel` and surfaced as `RoleView.AppliedModel`. The table SHALL ship an embedded default and SHALL allow a `config.toml` override to take precedence over the embedded default for any individual model entry. A model with no rate entry in either the override or the embedded default SHALL yield no cost figure for a role resolved to that model, rather than a zero or an approximated figure.

USD cost SHALL be computed at read time from the stored raw token totals against the CURRENTLY loaded rate table — never stored as a baked dollar amount — so correcting the rate table changes the displayed cost of already-recorded bindings immediately, with no re-scan of any transcript and no backfill.

Derived from: `internal/agent/agent.go` (`KnownModels`, `ResolveModel`), `internal/tui/hera_tiering.go` (`resolveHeraTier`, `RoleView.AppliedModel`).

#### Scenario: A curated model produces a computed cost

- **WHEN** a role is resolved to a model identifier present in the rate table (embedded default or config override)
- **THEN** its cost is computed from its token totals and that model's four rates

#### Scenario: An uncurated model produces no cost figure

- **WHEN** a role is resolved to a model identifier absent from both the config override and the embedded default rate table (e.g. an opencode/custom/Pi backend's free-form model string)
- **THEN** no cost figure is computed or displayed for that role — not zero, not an approximation

#### Scenario: A config override takes precedence over the embedded default

- **WHEN** `config.toml` provides a rate entry for a model that also has an embedded default
- **THEN** the configured rate is used, not the embedded default

#### Scenario: A rate-table update reprices already-recorded usage

- **WHEN** the rate table's entry for a model changes after bindings using that model have already accumulated token totals
- **THEN** those bindings' displayed cost reflects the NEW rate the next time it is computed, without any change to the stored token totals

### Requirement: Per-coordinator subtree cost rollup

The system SHALL compute a coordinator/orchestrator's total estimated cost as the sum of its own token-derived cost plus every role (coordinator, worker, or freelance) in its bridge subtree, walking the same subtree traversal `Model.SubtreeAgentCount` uses, and counting a nested sub-coordinator's cost exactly once via the same bridging-row convention that traversal already establishes (never once via its parent's bridging worker row AND again via its own orchestrator's coordinator role).

This rollup SHALL include a nuked role's recorded cost — in contrast to every other existing rollup consumer of role-listing (agent count, needs-input rollup), which excludes nuked roles because they are fully torn down from every display. The inclusion SHALL be implemented via a dedicated query path and SHALL NOT alter the behavior of `ListHeraRoles` or any of its other existing callers.

Derived from: `internal/tui/hera/model.go` (`Model.SubtreeAgentCount`, `Model.BridgeSubtree`), `internal/db/hera.go` (`ListHeraRoles`, whose unconditional `nuked_at IS NULL` filter this rollup's dedicated query path deliberately does not inherit).

#### Scenario: A subtree total includes a nuked child's recorded cost

- **WHEN** a coordinator's subtree includes a role that has since been nuked, and that role accrued nonzero token totals before being nuked
- **THEN** the coordinator's subtree cost total includes that nuked role's cost

#### Scenario: A nested sub-coordinator's cost is counted exactly once

- **WHEN** a root orchestrator bridges to a child sub-coordinator via a worker row, and that child sub-coordinator's own role also accrued cost
- **THEN** the root's subtree total counts the child sub-coordinator's cost exactly once, not twice

#### Scenario: Existing rollups are unaffected by the nuked-inclusive cost query

- **WHEN** the cost rollup's dedicated nuked-inclusive query path is added
- **THEN** `ListHeraRoles` and every other existing caller (agent count, needs-input rollup) continues to exclude nuked roles exactly as before this change

### Requirement: No retroactive cost; unmeasured is distinct from zero

A binding whose session predates this feature, or whose token totals have otherwise never been accumulated (all four token-total columns remain at their default of 0), SHALL be reported as having no cost data ("n/a" or equivalent absent-value display), never as a computed $0.00 cost. The system SHALL apply no backfill or migration to bindings recorded before this change ships.

Derived from: `internal/db/schema.go` (`hera_bindings` new columns, default 0, no backfill).

#### Scenario: An all-zero binding renders as unmeasured, not free

- **WHEN** a binding's four token-total columns are all exactly 0
- **THEN** its cost is reported as "n/a", not "$0.00"

#### Scenario: A pre-existing binding is never retroactively priced

- **WHEN** a binding was recorded before this change shipped and its transcript is no longer scanned
- **THEN** its token totals remain at their default of 0 and no cost is ever computed or backfilled for it
