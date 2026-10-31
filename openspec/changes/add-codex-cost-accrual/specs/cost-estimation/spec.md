## ADDED Requirements

### Requirement: Codex rollout accrual

The daemon SHALL accrue cost for every hera binding whose task's backend family declares the Codex-rollout cost source, by reading the Codex rollout files of every Codex thread whose recorded working directory equals the binding's worktree path. Threads SHALL be discovered through the Codex thread index (`state_5.sqlite` `threads` table) in the task's resolved Codex home (the task account's `codex_home`, otherwise the default Codex home). This set SHALL include Codex sub-agent threads and every thread started in that worktree, not only the thread whose ID Argus captured.

Each de-duplicated `token_count` event carrying a non-null `last_token_usage` SHALL be treated as one billable call. Consecutive events with an identical `total_token_usage` are duplicates and SHALL count once. A call's usage SHALL map onto the binding's existing raw columns as follows: `input_tokens − cached_input_tokens` → `tokens_input`; `cached_input_tokens` → `tokens_cache_read`; `cache_write_input_tokens` → `tokens_cache_write_5m`; `output_tokens` → `tokens_output`. `reasoning_output_tokens` SHALL NOT be added separately, because it is already included in `output_tokens`.

Each call SHALL be priced individually, at the moment it is first observed, against the rate table entry keyed by the model recorded in the rollout's most recent `turn_context` event preceding that call. The resulting USD and raw tokens SHALL be ADDED to the binding's `cost_usd_accrued` and raw token columns. A call whose recorded model has no rate-table entry SHALL advance the raw token columns but add no USD, and SHALL never be priced later.

The daemon SHALL run this accrual periodically, at least once per minute and once at daemon startup. It SHALL sweep bindings that are live or that ended within the last 15 minutes. It SHALL NOT touch bindings whose family declares any other cost source, so Codex accrual never interacts with Stop-hook or bridge accounting.

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

#### Scenario: Claude bindings are never swept

- **WHEN** a live hera binding's task runs on a backend whose family declares the Claude Stop-hook cost source
- **THEN** the Codex sweeper reads no rollout for it and leaves its cost columns and bridge baseline untouched

### Requirement: Codex accrual is incremental, idempotent, and binding-scoped

The daemon SHALL persist a read cursor per (binding, rollout file) recording the byte offset consumed, the last consumed event's running total (for cross-sweep de-duplication), and the most recent model seen. Each sweep SHALL read only bytes after the cursor, up to the last complete line; an incomplete trailing line SHALL be left for a later sweep. The binding's cost and raw-token increments and the cursor advance SHALL be committed in a single transaction, so a crash or daemon restart never prices a call twice and never skips one.

A call SHALL accrue to a binding only when the call's event timestamp is at or after the binding's `started_at` and, if the binding has ended, before its `ended_at`. A later binding on the same worktree SHALL therefore never re-accrue calls that belonged to an earlier binding, while still accruing later calls appended to the same rollout (for example, a resumed thread).

#### Scenario: Repeated sweeps over an unchanged rollout add nothing

- **WHEN** the sweeper runs twice with no new bytes appended to any of a binding's rollouts
- **THEN** the second sweep adds zero tokens and zero USD

#### Scenario: A torn final line is deferred, not lost

- **WHEN** a sweep reads a rollout whose last line is incomplete because Codex is mid-write
- **THEN** that line is not consumed, the cursor stops before it, and the next sweep prices it once it is complete

#### Scenario: Daemon restart resumes from the cursor

- **WHEN** the daemon restarts after a sweep committed a cursor for a rollout
- **THEN** the next sweep continues from that cursor and does not re-price earlier calls

#### Scenario: A rebinding does not double-count earlier calls

- **WHEN** a role's binding ends and a new binding for the same worktree starts, and the worktree's existing rollout contains calls from before the new binding's `started_at`
- **THEN** those earlier calls are not accrued to the new binding, and calls appended after the new binding's `started_at` are

## MODIFIED Requirements

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
