# cost-estimation Specification

## Purpose
TBD - created by archiving change add-coordinator-cost-estimate. Update Purpose after archive.
## Requirements
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

The system SHALL maintain a $/token rate table with five rate classes (fresh input, cache-write-1h, cache-write-5m, cache-read, output) per model identifier. Keys SHALL be either the model-alias strings already produced by `agent.ResolveModel` and surfaced as `RoleView.AppliedModel` (used by Claude accrual), or the provider-reported model identifier recorded in a session's own usage log (used by Codex rollout accrual, e.g. `gpt-6.1-sol`). A model with no rate entry SHALL yield no cost figure for usage priced under that model, for any accrual period observed while it lacked an entry, rather than a zero or an approximated figure.

A model entry MAY additionally declare a long-context tier: a `long_context_threshold` in input tokens plus a nested `long_context` block holding the same five rate classes. A pricer that prices individual calls SHALL apply the `long_context` rates to a call whose total input tokens (cached included) exceed the threshold, and the base rates otherwise. An entry with no threshold, or a threshold of 0, SHALL always use its base rates. Pricers that price aggregate deltas rather than individual calls (the Claude Stop-hook path) SHALL use the base rates.

The table SHALL be sourced from a `rates.toml` file, mirroring the diligence-profiles seed/install/precedence mechanism rather than an embedded Go map with a config-file override:

- A default `rates.toml` SHALL be committed in the repository and embedded into the binary at build time.
- The system SHALL install the embedded default to `~/.argus/rates.toml` when that path does not already exist, and SHALL NEVER overwrite an existing file at that path.
- An in-repo `rates.toml` (analogous to a profile's `RepoDir`) SHALL take precedence over the installed library copy when both are present.
- Unlike the profiles precedent's explicit-invocation-only install trigger, the system SHALL invoke the install step automatically (idempotently, at daemon startup or equivalent), since rate data is required for always-on accrual rather than an opt-in customization.
- Lookup SHALL re-read the file fresh on every access, with no caching and no separate reload mechanism, so a hand-edit takes effect on the next lookup.
- The embedded default SHALL include an entry for `gpt-6.1-sol` at OpenAI's published standard and long-context rates.

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

#### Scenario: A call above the long-context threshold uses long-context rates

- **WHEN** a per-call pricer prices a call with 300,000 total input tokens under a model whose entry declares `long_context_threshold = 272000` and a `long_context` block
- **THEN** every token class of that call is priced at the `long_context` rates

#### Scenario: A call at or below the threshold uses base rates

- **WHEN** a per-call pricer prices a call with exactly 272,000 total input tokens under the same model
- **THEN** the call is priced at the base rates

#### Scenario: A provider-reported model key is honored

- **WHEN** a Codex call is recorded under model `gpt-6.1-sol` and the rate table has a `gpt-6.1-sol` entry
- **THEN** the call is priced from that entry, regardless of the model alias Argus resolved for the task

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

One exception applies: Codex rollout accrual's one-time startup backfill (see "Codex rollout accrual") MAY price a never-read Codex binding that ended within the last 30 days, because Codex keeps its own per-call usage log on disk. Those calls are priced at the rate table in effect when they are first observed, the same accrual-time rule every other accrual follows. Claude transcript and bridge accrual remain non-retroactive.

Derived from: `internal/db/schema.go` (`hera_bindings` new columns, default 0, no backfill).

#### Scenario: An all-zero binding renders as unmeasured, not free

- **WHEN** a binding's five raw token-total columns and `cost_usd_accrued` are all exactly 0
- **THEN** its cost is reported as "n/a", not "$0.00"

#### Scenario: A pre-existing binding is never retroactively priced

- **WHEN** a binding was recorded before this change shipped and its transcript is no longer scanned
- **THEN** its totals remain at their default of 0 and no cost is ever computed or backfilled for it

#### Scenario: A recently ended Codex binding is the backfill exception

- **WHEN** a Codex binding ended within the last 30 days, was never read, and its rollouts are still on disk at daemon startup
- **THEN** its calls are priced once at the then-current rate table, and its cost no longer reads "n/a"

### Requirement: Bridge cumulative cost accrues once across resets

The daemon SHALL accrue session.measure cumulative USD cost on the binding using a persistent baseline. Duplicate measurements and daemon restart SHALL not double-count. A lower counter or session identity change SHALL start a new accrual epoch; previous accrued cost SHALL remain immutable.

A lower counter from the same known process generation and session SHALL be ignored as a late sample. A lower counter after a process restart SHALL start a new accrual epoch even when the transcript id is reused.

#### Scenario: Late old-boot measurement after daemon restart

- **WHEN** a lower cumulative sample arrives for the same process generation and session after a higher one was persisted
- **THEN** it adds no cost and does not lower the persistent baseline

On a source handoff, the first reading SHALL establish the new source's offset without adding cost. Under-counting a transition turn is accepted; double-counting is forbidden.

#### Scenario: Bridge becomes stale and later recovers

- **WHEN** accounting switches from bridge to transcript or back
- **THEN** the first new-source reading saves its baseline without adding cost and subsequent readings add only their incremental usage

#### Scenario: Increasing cost and duplicate delivery

- **WHEN** cost reports 1 USD then 1.5 USD then repeats 1.5 USD
- **THEN** the total added cost is 1.5 USD

#### Scenario: Clear or restart resets the counter

- **WHEN** a new session counter reports 0.2 USD after an old session accrued 1.5 USD
- **THEN** the binding total becomes 1.7 USD

### Requirement: Codex rollout accrual

The daemon SHALL accrue cost for every hera binding whose task's backend family declares the Codex-rollout cost source, or whose task row no longer exists (a deleted task cannot name its backend, so the presence of Codex threads in its worktree decides), by reading the Codex rollout files of every Codex thread whose recorded working directory equals the binding's worktree path. Threads SHALL be discovered through the Codex thread index (`state_5.sqlite` `threads` table) in the task's resolved Codex home (the task account's `codex_home`, otherwise the default Codex home; the default for a deleted task). This set SHALL include Codex sub-agent threads and every thread started in that worktree, not only the thread whose ID Argus captured.

Each de-duplicated `token_count` event carrying a non-null `last_token_usage` SHALL be treated as one billable call. Consecutive events with an identical `total_token_usage` are duplicates and SHALL count once. A call's usage SHALL map onto the binding's existing raw columns as follows: `input_tokens − cached_input_tokens` → `tokens_input`; `cached_input_tokens` → `tokens_cache_read`; `cache_write_input_tokens` → `tokens_cache_write_5m`; `output_tokens` → `tokens_output`. `reasoning_output_tokens` SHALL NOT be added separately, because it is already included in `output_tokens`.

Each call SHALL be priced individually, at the moment it is first observed, against the rate table entry keyed by the model recorded in the rollout's most recent `turn_context` event preceding that call. The resulting USD and raw tokens SHALL be ADDED to the binding's `cost_usd_accrued` and raw token columns. A call whose recorded model has no rate-table entry SHALL advance the raw token columns but add no USD, and SHALL never be priced later.

The daemon SHALL run this accrual periodically, at least once per minute and once at daemon startup. Each pass SHALL sweep bindings that are live or that ended within the last 15 minutes. It SHALL NOT touch bindings whose existing task's family declares any other cost source, so Codex accrual never interacts with Stop-hook or bridge accounting.

The first pass after daemon startup SHALL additionally backfill: every binding that ended within the last 30 days and has never been read (no cursor) SHALL be priced from the rollouts still on disk, under the same per-call rules. A binding with any cursor SHALL never be backfilled again, so each call is priced at most once across daemon restarts.

#### Scenario: A Codex worker's calls accrue cost

- **WHEN** a live hera binding's task runs on the Codex backend and its rollout contains a call recorded under model `gpt-6.1-sol` with 300,000 input tokens of which 290,000 are cached, and 2,000 output tokens, and the rate table has a `gpt-6.1-sol` entry
- **THEN** after the next sweep the binding's `tokens_input` increases by 10,000, `tokens_cache_read` by 290,000, `tokens_output` by 2,000, and `cost_usd_accrued` by that call's price

#### Scenario: Codex sub-agent threads are included

- **WHEN** a Codex worker spawns a Codex sub-agent whose thread is recorded in the thread index with the same working directory as the worker's worktree
- **THEN** the sub-agent thread's calls accrue to the worker's binding alongside the worker's own thread

#### Scenario: Duplicate token_count emissions count once

- **WHEN** a rollout contains two consecutive `token_count` events with identical `total_token_usage`
- **THEN** only one call is accrued for that pair

#### Scenario: Reasoning tokens are not double-counted

- **WHEN** a call reports `output_tokens` 500 and `reasoning_output_tokens` 200
- **THEN** the binding's `tokens_output` increases by 500, not 700

#### Scenario: A Codex call under an unpriced model advances tokens but not cost

- **WHEN** a Codex call is recorded under a model with no rate-table entry
- **THEN** the binding's raw token columns advance by that call's usage and `cost_usd_accrued` is unchanged, and adding a rate for that model later does not price that call

#### Scenario: A deleted task's Codex work still accrues

- **WHEN** a hera binding's task row has been deleted, and the default Codex home's thread index records Codex threads in that binding's worktree with calls inside the binding's window
- **THEN** those calls accrue to the binding

#### Scenario: Startup backfills a never-read ended binding once

- **WHEN** the daemon starts and a binding that ended 3 days ago has no cursor and its worktree's rollouts are still on disk
- **THEN** that pass prices the binding's calls, and no later pass or later daemon start prices them again

#### Scenario: Backfill stops at 30 days

- **WHEN** a never-read binding ended more than 30 days before the daemon starts
- **THEN** it is not backfilled

#### Scenario: Claude bindings are never swept

- **WHEN** a live hera binding's task runs on a backend whose family declares the Claude Stop-hook cost source
- **THEN** the Codex sweeper reads no rollout for it and leaves its cost columns and bridge baseline untouched

### Requirement: Codex accrual is incremental, idempotent, and binding-scoped

The daemon SHALL persist a read cursor per (binding, rollout file) recording the byte offset consumed, the last consumed event's running total (for cross-sweep de-duplication), and the most recent model seen. Each sweep SHALL read only bytes after the cursor, up to the last complete line; an incomplete trailing line SHALL be left for a later sweep. The binding's cost and raw-token increments and the cursor advance SHALL be committed in a single transaction, so a crash or daemon restart never prices a call twice and never skips one.

A call SHALL accrue to a binding only when the call's event timestamp is at or after the binding's `started_at` and, if the binding has ended, before its `ended_at`. A later binding on the same worktree SHALL therefore never re-accrue calls that belonged to an earlier binding, while still accruing later calls appended to the same rollout (for example, a resumed thread). A call whose timestamp falls inside the windows of two or more bindings of the same task (the task was bound under two or more orchestrators at that moment) SHALL accrue to none of them, because there is no single binding to charge. This mirrors the Stop-hook endpoint's ambiguous-binding rejection.

#### Scenario: Repeated sweeps over an unchanged rollout add nothing

- **WHEN** the sweeper runs twice with no new bytes appended to any of a binding's rollouts
- **THEN** the second sweep adds zero tokens and zero USD

#### Scenario: A torn final line is deferred, not lost

- **WHEN** a sweep reads a rollout whose last line is incomplete because Codex is mid-write
- **THEN** that line is not consumed, the cursor stops before it, and the next sweep prices it once it is complete

#### Scenario: Daemon restart resumes from the cursor

- **WHEN** the daemon restarts after a sweep committed a cursor for a rollout
- **THEN** the next sweep continues from that cursor and does not re-price earlier calls

#### Scenario: A call made while dual-bound is skipped

- **WHEN** a Codex-backend task is bound under two orchestrators at once and makes a call during that overlap
- **THEN** that call accrues to neither binding, while calls made when only one binding was live accrue to that one

#### Scenario: A rebinding does not double-count earlier calls

- **WHEN** a role's binding ends and a new binding for the same worktree starts, and the worktree's existing rollout contains calls from before the new binding's `started_at`
- **THEN** those earlier calls are not accrued to the new binding, and calls appended after the new binding's `started_at` are

