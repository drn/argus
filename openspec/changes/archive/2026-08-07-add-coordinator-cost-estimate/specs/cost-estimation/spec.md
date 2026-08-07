## ADDED Requirements

### Requirement: Stop-hook token-usage accumulation

The `argus coord-hook` Stop-hook subcommand SHALL, on every invocation that stamps `context_size` for any hera-bound role (coordinator, worker, or freelance alike), additionally scan `transcript_path` in full and compute five running totals — summed across EVERY main-chain (`type: "assistant"`, not `isSidechain`) message in the transcript — of `input_tokens`, `cache_read_input_tokens`, `output_tokens`, and the nested `usage.cache_creation.ephemeral_1h_input_tokens` / `usage.cache_creation.ephemeral_5m_input_tokens` fields (read as two SEPARATE running totals in place of the flattened `cache_creation_input_tokens` field `context_size`'s computation reads). Unlike `context_size` (which reflects only the LATEST such message's usage — a high-water-mark context-window gauge), these five totals SHALL be a SUM across every qualifying message, because Anthropic bills every API call independently for its own usage regardless of what a prior call already paid for.

The subcommand SHALL recompute all five totals afresh from the complete transcript on every invocation and OVERWRITE the persisted raw totals — it SHALL NOT add a delta to a previously stored raw total. This makes the raw-total computation idempotent against an unchanged transcript and immune to double-counting from a duplicate or retried Stop event. No bounded-retry-and-take-max logic (the mechanism `context_size` needs to judge a single scan's freshness against transcript_path's async-write lag) is required for these sums: an under-count from a transcript write that has not yet landed self-corrects on the very next invocation, with no risk of ever over-counting. (The separately-persisted dollar figure derived from these raw totals has its OWN, different update rule — see "Accrual-time cost stamping" below.)

A transcript line whose flat `cache_creation_input_tokens` is nonzero but whose nested `cache_creation` object is absent SHALL be attributed to the 5-minute-TTL running total (the non-opted-in default) as an accepted approximation, rather than blocking accumulation for that line.

Derived from: `cmd/argus/coord_hook.go` (`scanContextSize`, the sibling function this accumulation extends alongside).

#### Scenario: Sidechain messages are excluded from the sum

- **WHEN** a transcript contains a line with `isSidechain: true` (a dispatched sub-agent's own turn)
- **THEN** that line's usage is excluded from all five running totals, mirroring `context_size`'s existing exclusion

#### Scenario: An unchanged transcript reproduces the same raw totals

- **WHEN** the hook fires twice with no new turns landing in the transcript between invocations
- **THEN** both invocations compute identical values for all five raw totals

#### Scenario: Accumulation runs for every hera-bound role kind

- **WHEN** the hook fires in a session bound to a `worker`-kind or `freelance`-kind role
- **THEN** the five token totals are computed and persisted exactly as they would be for a `coordinator`-kind role, independent of the coordinator-only budget/nudge logic that gates on kind elsewhere

#### Scenario: Output tokens are captured

- **WHEN** a main-chain assistant message's usage includes a nonzero `output_tokens` value
- **THEN** that value contributes to the running output-token total, a field `context_size`'s own computation never reads

#### Scenario: Cache-write tokens are split by TTL tier, not blended

- **WHEN** a main-chain assistant message's usage includes a nonzero nested `cache_creation.ephemeral_1h_input_tokens` and/or `cache_creation.ephemeral_5m_input_tokens`
- **THEN** each contributes to its own separate running total (1-hour vs 5-minute), and the flattened `cache_creation_input_tokens` field is not read for this computation

#### Scenario: A missing per-TTL breakdown falls back to the 5-minute tier

- **WHEN** a main-chain assistant message's usage has a nonzero flat `cache_creation_input_tokens` but no nested `cache_creation` object
- **THEN** that value is added to the 5-minute-TTL running total as an accepted approximation

### Requirement: Token totals persist on the binding, independent of task archiving

The system SHALL persist the five running raw token totals as columns on the task's currently-live `hera_bindings` row (`tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output`), written via a hera-scoped REST endpoint distinct from the generic `task_meta` write path the hook uses for `context_size`. Archiving the underlying argus task SHALL NOT delete or reset a binding's token totals or its accrued cost (see "Accrual-time cost stamping" below, which persists on the same row).

A role's total token usage SHALL be computed as the sum of its five columns across every one of that role's bindings — both the live binding (if any) and every prior ended binding — covering a role that has been re-bound or recycled over its lifetime.

Derived from: `internal/db/schema.go` (`hera_bindings`), `internal/db/hera.go` (`ListHeraBindingsByRole`), `internal/db/task_meta.go` (`DeleteMetaForTask`, the archive-coupled deletion this design deliberately avoids by not using `task_meta`).

#### Scenario: Archiving a task leaves its binding's token totals unchanged

- **WHEN** an argus task whose live-at-the-time hera binding carries nonzero token totals is archived
- **THEN** that binding's five token-total columns remain unchanged after archiving

#### Scenario: A recycled role's total spans every incarnation

- **WHEN** a role has been re-bound (ended one binding, started a new one) one or more times over its lifetime
- **THEN** the role's total token usage sums the five columns across every one of its bindings, live and ended alike

### Requirement: Deterministic five-class per-model rate table, seeded and installed like diligence profiles

The system SHALL maintain a $/token rate table with five rate classes (fresh input, cache-write-1h, cache-write-5m, cache-read, output) per model identifier, keyed by the same model-alias strings already produced by `agent.ResolveModel` and surfaced as `RoleView.AppliedModel`. A model with no rate entry SHALL yield no cost figure for a role resolved to that model, for any accrual period observed while it lacked an entry, rather than a zero or an approximated figure.

The table SHALL be sourced from a `rates.toml` file, mirroring the diligence-profiles seed/install/precedence mechanism rather than an embedded Go map with a config-file override: a default `rates.toml` SHALL be committed in the repository and embedded into the binary at build time; the system SHALL install the embedded default to `~/.argus/rates.toml` when that path does not already exist, and SHALL NEVER overwrite an existing file at that path; an in-repo `rates.toml` (analogous to a profile's `RepoDir`) SHALL take precedence over the installed library copy when both are present. Unlike the profiles precedent's explicit-invocation-only install trigger, the system SHALL invoke the install step automatically (idempotently, at daemon startup or equivalent) since rate data is required for the always-on Stop-hook accrual mechanism rather than an opt-in customization. Lookup SHALL re-read the file fresh on every access, with no caching and no separate reload mechanism — a hand-edit takes effect on the next lookup.

Derived from: `internal/agent/agent.go` (`KnownModels`, `ResolveModel`), `internal/tui/hera_tiering.go` (`resolveHeraTier`, `RoleView.AppliedModel`), `internal/profiles/seeds.go` (`seedFS`, `SeedNames`), `internal/profiles/install.go` (`InstallDefaults`), `internal/profiles/load.go` (`Loader.locate`, `Loader.Load` — the pattern this requirement mirrors).

#### Scenario: A curated model produces a computed cost

- **WHEN** a role is resolved to a model identifier present in the rate table
- **THEN** its accrued cost is computed from its token-total deltas and that model's five rates, at the time each delta is observed

#### Scenario: An uncurated model produces no cost figure

- **WHEN** a role is resolved to a model identifier absent from the rate table (e.g. an opencode/custom/Pi backend's free-form model string)
- **THEN** no cost is accrued for that role for as long as it lacks a rate entry — not zero, not an approximation

#### Scenario: An existing rates.toml is never overwritten

- **WHEN** `~/.argus/rates.toml` already exists (an operator has hand-edited it, or a prior daemon startup already installed it)
- **THEN** the install step leaves it untouched, regardless of how it differs from the embedded default

#### Scenario: An in-repo rates.toml takes precedence

- **WHEN** both an in-repo `rates.toml` and the installed `~/.argus/rates.toml` are present
- **THEN** the in-repo copy is used for rate lookups

#### Scenario: A hand-edit takes effect without a restart

- **WHEN** an operator edits `~/.argus/rates.toml` (or the in-repo override) directly
- **THEN** the very next accrual-time pricing lookup reflects the edit, with no daemon restart and no explicit reload call

### Requirement: Accrual-time cost stamping — historical cost is immutable

The system SHALL compute and persist a running dollar total, `cost_usd_accrued`, on the same `hera_bindings` row as the raw token totals, updated INCREMENTALLY rather than recomputed and overwritten wholesale. On each Stop-hook invocation that produces fresh raw token totals (per "Stop-hook token-usage accumulation"), the system SHALL: (1) read the binding's previously-persisted five raw totals and its previous `cost_usd_accrued`; (2) compute the delta between the newly-observed raw totals and the previous ones, per rate class; (3) price that delta against the rate table AS IN EFFECT AT THAT MOMENT; (4) add the resulting amount to `cost_usd_accrued`; (5) persist the new raw totals and the new `cost_usd_accrued` together.

A later change to the rate table SHALL affect only deltas priced AFTER the change — it SHALL NOT alter any `cost_usd_accrued` value already persisted from a prior delta. A duplicate or retried Stop-hook invocation that observes no change in the raw totals SHALL leave `cost_usd_accrued` unchanged (a zero delta prices to zero). A delta observed while a role's resolved model has no rate-table entry SHALL be permanently excluded from `cost_usd_accrued` — it SHALL NOT be retroactively priced if a rate for that model is added later.

Derived from: the daemon-side handler for the REST endpoint in "Token totals persist on the binding" — this requirement governs that handler's pricing logic specifically.

#### Scenario: A rate change does not alter already-accrued cost

- **WHEN** the rate table's entry for a model changes after a binding using that model has already accrued cost under the old rate
- **THEN** that binding's `cost_usd_accrued` value from before the change is unaffected — only deltas observed after the change use the new rate

#### Scenario: A duplicate hook invocation adds no cost

- **WHEN** the Stop hook fires twice against a transcript that has not grown between invocations
- **THEN** the second invocation's raw-total delta is zero for every rate class, and `cost_usd_accrued` is unchanged

#### Scenario: A rate added later does not retroactively price earlier usage

- **WHEN** a role accrues usage while its resolved model has no rate-table entry, and a rate for that model is added afterward
- **THEN** the usage observed before the rate existed is never priced into `cost_usd_accrued`, even after the rate is added — only usage observed after the rate exists is priced

### Requirement: Per-coordinator subtree cost rollup

The system SHALL compute a coordinator/orchestrator's total estimated cost as the sum of its own `cost_usd_accrued` plus every role's (coordinator, worker, or freelance) `cost_usd_accrued` in its bridge subtree, walking the same subtree traversal `Model.SubtreeAgentCount` uses, and counting a nested sub-coordinator's cost exactly once via the same bridging-row convention that traversal already establishes (never once via its parent's bridging worker row AND again via its own orchestrator's coordinator role). Because `cost_usd_accrued` is already fully priced at accrual time, this rollup SHALL be pure addition — it SHALL NOT perform any rate-table lookup or repricing at rollup time.

This rollup SHALL include a nuked role's recorded cost — in contrast to every other existing rollup consumer of role-listing (agent count, needs-input rollup), which excludes nuked roles because they are fully torn down from every display. The inclusion SHALL be implemented via a DEDICATED, new query path, distinct from `ListHeraRoles` — `ListHeraRoles`'s existing signature and behavior SHALL be left unmodified for every other caller.

Derived from: `internal/tui/hera/model.go` (`Model.SubtreeAgentCount`, `Model.BridgeSubtree`), `internal/db/hera.go` (`ListHeraRoles`, whose unconditional `nuked_at IS NULL` filter this rollup's dedicated query path deliberately does not inherit).

#### Scenario: A subtree total includes a nuked child's recorded cost

- **WHEN** a coordinator's subtree includes a role that has since been nuked, and that role accrued nonzero cost before being nuked
- **THEN** the coordinator's subtree cost total includes that nuked role's `cost_usd_accrued`

#### Scenario: A nested sub-coordinator's cost is counted exactly once

- **WHEN** a root orchestrator bridges to a child sub-coordinator via a worker row, and that child sub-coordinator's own role also accrued cost
- **THEN** the root's subtree total counts the child sub-coordinator's cost exactly once, not twice

#### Scenario: Existing rollups are unaffected by the nuked-inclusive cost query

- **WHEN** the cost rollup's dedicated nuked-inclusive query path is added
- **THEN** `ListHeraRoles` and every other existing caller (agent count, needs-input rollup) continues to exclude nuked roles exactly as before this change, via a wholly separate function rather than a parameter added to `ListHeraRoles`

### Requirement: No retroactive cost; unmeasured is distinct from zero

A binding whose session predates this feature, or whose totals have otherwise never been accumulated (all five raw token-total columns AND `cost_usd_accrued` remain at their default of 0), SHALL be reported as having no cost data ("n/a" or equivalent absent-value display), never as a computed $0.00 cost. The system SHALL apply no backfill or migration to bindings recorded before this change ships, nor to any accrual period whose model lacked a rate-table entry at the time (see "Accrual-time cost stamping").

Derived from: `internal/db/schema.go` (`hera_bindings` new columns, default 0, no backfill).

#### Scenario: An all-zero binding renders as unmeasured, not free

- **WHEN** a binding's five raw token-total columns and `cost_usd_accrued` are all exactly 0
- **THEN** its cost is reported as "n/a", not "$0.00"

#### Scenario: A pre-existing binding is never retroactively priced

- **WHEN** a binding was recorded before this change shipped and its transcript is no longer scanned
- **THEN** its totals remain at their default of 0 and no cost is ever computed or backfilled for it
