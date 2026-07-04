# diligence-profiles Specification

## Purpose
TBD - created by archiving change add-diligence-profiles. Update Purpose after archive.
## Requirements
### Requirement: Profile file format and discovery

A diligence profile SHALL be a single TOML file named `<name>.toml`, discovered from a per-user library
directory `~/.argus/profiles/` and, optionally, from an in-repo `.argus/profiles/` directory within a
project worktree. When a profile name exists in both locations, the in-repo file SHALL take precedence
over the per-user library file. The system SHALL store and reference profiles by **name only**; profile
bodies SHALL NOT be persisted in the database.

#### Scenario: Load from the per-user library

- **WHEN** a profile `lean` exists at `~/.argus/profiles/lean.toml` and no in-repo file shadows it
- **THEN** loading `lean` returns the parsed contents of the per-user file

#### Scenario: In-repo file takes precedence

- **WHEN** a profile `lean` exists at both `~/.argus/profiles/lean.toml` and the worktree's
  `.argus/profiles/lean.toml`
- **THEN** loading `lean` returns the in-repo file's contents and reports the in-repo source

#### Scenario: Source is reported

- **WHEN** a profile is loaded
- **THEN** the result identifies whether the name resolved from the in-repo directory or the per-user
  library

### Requirement: Profile structure and archetypes

A profile SHALL describe, per archetype, EITHER an optional scalar `model` and `effort` (a single fixed pair, as before) OR an ordered `menu` of `{model, effort}` pairs — never both on the same archetype table — plus an optional `window`, a `[rigor]` table (`review_passes`, `gating`, `security_spot_check`), and an opaque `[panel]` table. `window` applies to the archetype as a whole regardless of which form is used. The system SHALL recognize exactly thirteen canonical archetypes: `brainstorm`, `orchestrator`, `big_build`, `code_slice`, `bug_fix`, `review`, `security_review`, `synthesis`, `spec_audit`, `ci_loop`, `verify`, `recovery`, `docs`.

#### Scenario: Per-archetype fields parse

- **WHEN** a profile declares `[archetype.code_slice]` with `model = "sonnet"`
- **THEN** the loaded profile exposes `sonnet` as the `code_slice` archetype's model

#### Scenario: Menu form parses

- **WHEN** a profile declares `[archetype.code_slice]` with `menu = [{ model = "sonnet", effort = "high" },
  { model = "opus", effort = "low" }]`
- **THEN** the loaded profile exposes the two-entry ordered menu for `code_slice`, in file order

#### Scenario: Rigor flags parse

- **WHEN** a profile declares a `[rigor]` table with `review_passes`, `gating`, and `security_spot_check`
- **THEN** the loaded profile exposes those rigor flags

### Requirement: Profile inheritance

A profile MAY declare `extends = "<parent>"`. Resolving a profile SHALL overlay the child's declared
fields onto the fully-resolved parent, recursively, so a child overrides only the fields it sets. A
`default` profile SHALL exist as the resolution target for projects with no explicit binding.

#### Scenario: Child overlays parent

- **WHEN** `lean` extends `default` and sets only `[archetype.code_slice].model`
- **THEN** the resolved `lean` profile carries `default`'s values for every other archetype and `lean`'s
  override for `code_slice`

#### Scenario: Unmapped project resolves default

- **WHEN** a project has no profile binding
- **THEN** resolution targets the `default` profile

### Requirement: Profile validation

The system SHALL validate that a profile conforms: every archetype table names a canonical archetype; each `effort` (scalar, or per-entry within a `menu`) is one of `low`/`medium`/`high`/`xhigh`/`max`; each `window` is one of `200k`/`1m`; every `model` (scalar, or per-entry within a `menu`) is a member of the union of built-in backend aliases and every configured backend's `models` list; an archetype SHALL NOT set both a scalar `model`/`effort` and a `menu`; a `menu`, when present, SHALL contain at least two entries; the `extends` chain terminates without a cycle; and any `[panel]` table is structurally well-formed. Validation SHALL report all conformance errors found, not just the first.

#### Scenario: Unknown archetype rejected

- **WHEN** a profile declares `[archetype.planner]` (not a canonical archetype)
- **THEN** validation reports an unknown-archetype error

#### Scenario: Out-of-enum effort or window rejected

- **WHEN** a profile sets `effort = "critical"` or `window = "2m"`
- **THEN** validation reports the offending field and its allowed values

#### Scenario: Unknown model rejected

- **WHEN** a profile names a model present in neither the built-in aliases nor any configured backend's
  `models` list
- **THEN** validation reports the unknown model

#### Scenario: Backend-contributed model accepted

- **WHEN** a configured backend declares `models = ["gemini-2.5-pro"]` and a profile names
  `gemini-2.5-pro`
- **THEN** validation accepts the model

#### Scenario: Extends cycle rejected

- **WHEN** profile `a` extends `b` and `b` extends `a`
- **THEN** validation reports an inheritance cycle

#### Scenario: Opaque panel accepted structurally

- **WHEN** a profile carries a structurally well-formed `[panel]` table
- **THEN** validation accepts it without validating its composition grammar

#### Scenario: Menu and scalar fields together rejected

- **WHEN** a profile declares `[archetype.code_slice]` with both `model = "sonnet"` and a `menu`
- **THEN** validation reports a mutual-exclusivity error naming the archetype

#### Scenario: Single-entry menu rejected

- **WHEN** a profile declares a `menu` with exactly one entry
- **THEN** validation reports the menu as too short to express a choice

#### Scenario: Menu entry validated per-field

- **WHEN** a menu entry names an unknown model or an out-of-enum effort
- **THEN** validation reports the offending entry the same way it reports a scalar field error

### Requirement: Reviewer-panel forward-reference seam

The `[panel]` table SHALL be retained verbatim by the loader as an opaque block whose composition
grammar is owned by a separate capability. This capability SHALL NOT define, constrain, or interpret the
panel's model/lens roster or synthesizer contract; it SHALL only confirm the block's structural shape.

#### Scenario: Panel block retained without interpretation

- **WHEN** a profile is loaded with a `[panel]` table
- **THEN** the panel block is available to consumers verbatim and is not otherwise interpreted by this
  capability

### Requirement: Profile-aware model resolution

The system SHALL resolve a task's effective model and effort TOGETHER, as a pair, with the precedence: the per-task model/effort override when set (independently per field — see Menu-based archetype resolution and governance for how a full pair is validated when the resolved archetype is a menu); otherwise, when the task carries an archetype and a valid profile is present, the profile's resolved model/effort pair for that archetype (its scalar pair, or — for a menu archetype — the pair chosen per the menu governance requirement); otherwise the project/backend default (no default effort exists; effort resolves to empty when nothing else supplies it). The profile consulted is, in order: the task's per-spawn `profile` override (when non-empty), else the project's bound profile, else `default`. When the consulted profile is missing or invalid, or the resolved model is not valid for the task's resolved backend, the system SHALL resolve to **no model** (no `--model` injected) and **no effort** rather than failing the spawn. A task that carries no archetype SHALL NOT consult any profile. When an effort resolves, the system SHALL inject it per-backend: `--effort <level>` for Claude-style backends, `-c model_reasoning_effort=<level>` for codex, and no flag for pi/unknown/custom backends.

#### Scenario: Task model override wins

- **WHEN** a task sets `Model = "opus"` and also has an archetype with a bound profile
- **THEN** the effective model is `opus` and the profile is not consulted

#### Scenario: Per-spawn profile override honored

- **WHEN** a task's `profile` field is non-empty (set at spawn as a per-spawn override)
- **THEN** resolution consults that profile instead of the project's bound profile

#### Scenario: Empty per-spawn override uses project binding

- **WHEN** a task's `profile` field is empty
- **THEN** resolution falls through to the project's bound profile (or `default` if unbound)

#### Scenario: Profile model applied by archetype

- **WHEN** a task has no model override, carries archetype `code_slice`, and the resolved profile is valid
- **THEN** the effective model is the profile's `code_slice` model

#### Scenario: Invalid-for-backend model falls through

- **WHEN** the profile's archetype model is not valid for the task's resolved backend
- **THEN** resolution falls through to the project/backend default rather than injecting an invalid model

#### Scenario: Missing or invalid profile falls open

- **WHEN** the consulted profile is missing or fails validation
- **THEN** resolution injects no `--model` and no effort, and the spawn proceeds with the CLI's own default

#### Scenario: No archetype skips the profile

- **WHEN** a task carries no archetype
- **THEN** resolution does not consult any profile and uses the existing task/project/backend default

#### Scenario: Effort resolved and injected for claude

- **WHEN** a task's archetype resolves effort `high` for a Claude-style backend
- **THEN** the command includes `--effort high`

#### Scenario: Effort resolved and injected for codex

- **WHEN** a task's archetype resolves effort `high` for a codex backend
- **THEN** the command includes `-c model_reasoning_effort=high`

#### Scenario: No effort injection for unsupported backends

- **WHEN** a task's archetype resolves an effort for a pi or unknown/custom backend
- **THEN** the command includes no effort flag or override

#### Scenario: Existing hand-edited effort flag wins

- **WHEN** the backend command already names an effort flag or `model_reasoning_effort` override
- **THEN** resolution injects no further effort flag

### Requirement: Profile environment injection

When a profile resolves for a spawned agent, the system SHALL export `ARGUS_PROFILE` (the bound profile name), `ARGUS_ARCHETYPE` (the task's archetype), `ARGUS_MODEL` (the resolved model), and `ARGUS_EFFORT` (the resolved effort) into the agent's environment alongside the existing task-ID export. These four are exported together or not at all. When no profile resolves, all four SHALL be omitted rather than exported empty.

#### Scenario: Vars exported on resolution

- **WHEN** a task with archetype `code_slice` resolves a valid bound profile
- **THEN** the spawned agent's environment includes `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, `ARGUS_MODEL`, and
  `ARGUS_EFFORT`

#### Scenario: Vars omitted without a profile

- **WHEN** a task carries no archetype or no profile resolves
- **THEN** the profile environment variables are absent from the spawned agent's environment

### Requirement: Profile validation CLI affordance

The system SHALL expose a command-line `validate` affordance that, given a profile name, loads and
validates the profile (resolving inheritance and the in-repo/library source) and reports every
conformance error or confirms the profile is valid. The affordance SHALL be documentation/operator
tooling only and SHALL NOT be wired into the Go build, CI, or any Make gate.

#### Scenario: Valid profile reported

- **WHEN** `validate` runs against a conforming profile
- **THEN** it reports the profile valid and names the source it resolved from

#### Scenario: Invalid profile reports all errors

- **WHEN** `validate` runs against a profile with multiple conformance errors
- **THEN** it reports each error and exits non-zero

### Requirement: Seed profiles

The change SHALL ship three example profiles — `default`, `lean`, and `customer_grade` — seeded from the archetype→model framework (premium for high-leverage/low-verifiability roles, cheap for verifiable high-volume roles), with Fable treated as absent. `default`'s `code_slice` archetype SHALL be authored as a `menu` (at least two ordered `{model, effort}` pairs) to demonstrate the menu form, since job difficulty genuinely varies for that archetype; every other seeded archetype SHALL keep the scalar form. These SHALL be provided as documented examples for the operator to install or adapt, not auto-written into `~/.argus/profiles/`.

#### Scenario: Default seed covers all archetypes

- **WHEN** the `default` seed profile is validated
- **THEN** it conforms and provides a model (scalar or menu) for each canonical archetype

#### Scenario: Default seed's code_slice is a menu

- **WHEN** the `default` seed profile's `code_slice` archetype is inspected
- **THEN** it is authored as an ordered menu of at least two `{model, effort}` pairs, cheapest first

#### Scenario: Lean and customer_grade extend default

- **WHEN** the `lean` and `customer_grade` seed profiles are validated
- **THEN** they conform and express their differences as overrides of `default`

### Requirement: Menu-based archetype resolution and governance

When a task's archetype resolves (via the profile consultation order in Profile-aware model resolution) to an archetype authored as a `menu`, the system SHALL apply governance in addition to the base precedence: when both `task.Model` and `task.Effort` are explicitly set, the system SHALL validate the pair against the menu's entries — a matching pair SHALL be honored, and a non-matching pair SHALL be replaced with the menu's first (cheapest) entry, with the substitution logged. When only one of `task.Model`/`task.Effort` is explicitly set, the system SHALL honor that field as-is and default the unset field from the menu's first entry, without a membership check. When neither is explicitly set, the system SHALL default to the menu's first entry. This governance SHALL apply identically whether the spawn is a direct `hera_spawn_worker` call or a plan-DAG planned-node materialization. A task carrying no archetype, an archetype absent from any valid resolved profile, or a scalar (non-menu) archetype SHALL NOT be subject to this governance — its `task.Model`/`task.Effort` overrides resolve exactly as described in Profile-aware model resolution, with no membership check of any kind.

#### Scenario: Matching full override honored

- **WHEN** a task explicitly sets `Model = "opus"` and `Effort = "low"`, and its resolved archetype's menu
  contains `{opus, low}`
- **THEN** the effective model/effort is `opus`/`low`

#### Scenario: Non-matching full override substituted and logged

- **WHEN** a task explicitly sets `Model = "opus"` and `Effort = "high"`, and its resolved archetype's
  menu does not contain `{opus, high}`
- **THEN** the effective model/effort is the menu's first entry, and the substitution is logged

#### Scenario: Partial override fills the unset field from the cheapest entry

- **WHEN** a task explicitly sets only `Model = "opus"` (menu-resolved archetype, `Effort` unset)
- **THEN** the effective model is `opus` and the effective effort is the menu's first entry's effort, with
  no membership check performed

#### Scenario: No override defaults to the cheapest entry

- **WHEN** a task sets neither `Model` nor `Effort` and its archetype resolves to a menu
- **THEN** the effective model/effort is the menu's first entry

#### Scenario: Plan-DAG materialization defaults identically

- **WHEN** a planned hera role with a menu-resolved archetype is materialized with no explicit
  model/effort override
- **THEN** the effective model/effort is the menu's first entry, the same as a direct spawn with no
  override

#### Scenario: Scalar archetype is never gated

- **WHEN** a task's resolved archetype is a scalar (non-menu) value
- **THEN** any `task.Model`/`task.Effort` override is honored unconditionally, with no menu-membership
  check

