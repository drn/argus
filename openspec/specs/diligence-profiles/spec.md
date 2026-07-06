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

A profile SHALL describe, per archetype, an optional `model`, `effort`, and `window`, plus a `[rigor]`
table (`review_passes`, `gating`, `security_spot_check`) and an opaque `[panel]` table. The system SHALL
recognize exactly thirteen canonical archetypes: `brainstorm`, `orchestrator`, `big_build`,
`code_slice`, `bug_fix`, `review`, `security_review`, `synthesis`, `spec_audit`, `ci_loop`, `verify`,
`recovery`, `docs`.

#### Scenario: Per-archetype fields parse

- **WHEN** a profile declares `[archetype.code_slice]` with `model = "sonnet"`
- **THEN** the loaded profile exposes `sonnet` as the `code_slice` archetype's model

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

The system SHALL validate that a profile conforms: every archetype table names a canonical archetype; each `effort` is one of `low`/`medium`/`high`; each `window` is one of `200k`/`1m`; every `model` is a member of the union of built-in backend aliases and every configured backend's `models` list; the `extends` chain terminates without a cycle; and the `[panel]` table passes the injected reviewer-panel-grammar validator when one is supplied, falling back to structural well-formedness when it is not. Validation SHALL report all conformance errors found, not just the first.

#### Scenario: Unknown archetype rejected

- **WHEN** a profile declares `[archetype.planner]` (not a canonical archetype)
- **THEN** validation reports an unknown-archetype error

#### Scenario: Out-of-enum effort or window rejected

- **WHEN** a profile sets `effort = "max"` or `window = "2m"`
- **THEN** validation reports the offending field and its allowed values

#### Scenario: Unknown model rejected

- **WHEN** a profile names a model present in neither the built-in aliases nor any configured backend's `models` list
- **THEN** validation reports the unknown model

#### Scenario: Backend-contributed model accepted

- **WHEN** a configured backend declares `models = ["gemini-2.5-pro"]` and a profile names `gemini-2.5-pro`
- **THEN** validation accepts the model

#### Scenario: Extends cycle rejected

- **WHEN** profile `a` extends `b` and `b` extends `a`
- **THEN** validation reports an inheritance cycle

#### Scenario: Panel validated by the injected grammar validator

- **WHEN** a panel-grammar validator is injected and a profile carries a malformed `[panel]` table
- **THEN** validation reports the panel-grammar error

#### Scenario: Panel structural fallback without a validator

- **WHEN** no panel-grammar validator is injected and a profile carries a structurally well-formed `[panel]` table
- **THEN** validation accepts it on structural shape alone

### Requirement: Reviewer-panel forward-reference seam

The `[panel]` table SHALL be retained verbatim by the loader as a block whose composition grammar is defined by the `cross-vendor-review` capability. The `diligence-profiles` capability SHALL NOT hard-code the panel's grammar; instead it SHALL accept an injected panel-grammar validator (mirroring the injected known-models function) and apply it during validation when supplied, so that panel semantics live with the owning capability and no `profiles → review` import dependency is introduced.

#### Scenario: Panel block retained for consumers

- **WHEN** a profile is loaded with a `[panel]` table
- **THEN** the panel block is available to consumers verbatim

#### Scenario: Grammar enforced only via injection

- **WHEN** the panel-grammar validator is injected
- **THEN** panel conformance is enforced by that validator, and the profiles package itself does not import the review capability

### Requirement: Profile-aware model resolution

The system SHALL resolve a task's effective model with the precedence: the per-task model override when
set; otherwise, when the task carries an archetype and a valid profile is present, the profile's model for
that archetype; otherwise the project/backend default. The profile consulted is, in order: the task's
per-spawn `profile` override (when non-empty), else the project's bound profile, else `default`. When the
consulted profile is missing or invalid, or the resolved profile model is not valid for the task's
resolved backend, the system SHALL resolve to **no model** (no `--model` injected) rather than failing the
spawn. A task that carries no archetype SHALL NOT consult any profile.

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
- **THEN** resolution injects no `--model` and the spawn proceeds with the CLI's own default

#### Scenario: No archetype skips the profile

- **WHEN** a task carries no archetype
- **THEN** resolution does not consult any profile and uses the existing task/project/backend default

### Requirement: Profile environment injection

When a profile resolves for a spawned agent, the system SHALL export `ARGUS_PROFILE` (the bound profile
name), `ARGUS_ARCHETYPE` (the task's archetype), and `ARGUS_MODEL` (the resolved model) into the agent's
environment alongside the existing task-ID export. When no profile resolves, these variables SHALL be
omitted rather than exported empty.

#### Scenario: Vars exported on resolution

- **WHEN** a task with archetype `code_slice` resolves a valid bound profile
- **THEN** the spawned agent's environment includes `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, and `ARGUS_MODEL`

#### Scenario: Vars omitted without a profile

- **WHEN** a task carries no archetype or no profile resolves
- **THEN** the profile environment variables are absent from the spawned agent's environment

### Requirement: Profile validation CLI affordance

The system SHALL expose a command-line `validate` affordance that, given a profile name, loads and
validates the profile (resolving inheritance and the in-repo/library source) and reports every
conformance error or confirms the profile is valid. The affordance SHALL inject the same
reviewer-panel-grammar validator (`internal/review.NewValidator`) that the daemon-side/MCP consumption
callers inject, so a malformed `[panel]` table is reported at `argus validate` — not only at spawn-time
resolution or `profile_resolve` (this closes the gap where a `[panel]` typo could pass `argus validate`
clean while silently fail-opening the profile's entire archetype/rigor tiering at spawn; see design.md's
Open Question #2 resolution). The affordance SHALL be documentation/operator tooling only and SHALL NOT
be wired into the Go build, CI, or any Make gate.

#### Scenario: Valid profile reported

- **WHEN** `validate` runs against a conforming profile
- **THEN** it reports the profile valid and names the source it resolved from

#### Scenario: Invalid profile reports all errors

- **WHEN** `validate` runs against a profile with multiple conformance errors
- **THEN** it reports each error and exits non-zero

#### Scenario: CLI reports a malformed panel

- **WHEN** `argus validate` runs against a profile whose `[panel]` table fails the reviewer-panel-grammar validator (e.g. an unknown finder id)
- **THEN** it reports the panel-grammar error and exits non-zero, the same as any other conformance error

### Requirement: Seed profiles

The change SHALL ship three example profiles — `default`, `lean`, and `customer_grade` — seeded from the
archetype→model framework (premium for high-leverage/low-verifiability roles, cheap for verifiable
high-volume roles), with Fable treated as absent. These SHALL be provided as documented examples for the
operator to install or adapt, not auto-written into `~/.argus/profiles/`.

#### Scenario: Default seed covers all archetypes

- **WHEN** the `default` seed profile is validated
- **THEN** it conforms and provides a model for each canonical archetype

#### Scenario: Lean and customer_grade extend default

- **WHEN** the `lean` and `customer_grade` seed profiles are validated
- **THEN** they conform and express their differences as overrides of `default`

### Requirement: Agent-facing profile resolution

The system SHALL expose an `mcp__argus__profile_resolve` MCP tool that resolves the diligence profile in effect for a caller and returns the fully-resolved profile body as structured JSON. Resolution SHALL run daemon-side (the daemon can read `~/.argus/profiles`, which a sandboxed agent cannot) and SHALL reuse the existing `internal/profiles` loading, in-repo precedence, `extends` overlay, and validation rather than re-implementing them. The returned body SHALL carry the per-archetype entries, the `[rigor]` block, and the `[panel]` block, with per-archetype entries passed through verbatim (not collapsed to single scalars) so a future per-archetype model-menu extension does not break the contract. Resolution SHALL fail open: a missing or invalid profile returns a structured "unresolved" result carrying the validation errors, never a hard tool error.

#### Scenario: Resolve by working directory

- **WHEN** `profile_resolve` is called with a `cwd` that maps to a project with a bound profile
- **THEN** it returns the fully-resolved profile body (archetype entries, `[rigor]`, `[panel]`) as structured JSON

#### Scenario: Per-spawn override precedence

- **WHEN** the resolving task carries a non-empty per-spawn profile override
- **THEN** the override name is resolved in preference to the project's bound profile

#### Scenario: Explicit profile name for testing

- **WHEN** `profile_resolve` is called with an explicit profile-name argument
- **THEN** it resolves that named profile directly, bypassing `cwd`→project resolution

#### Scenario: Missing or invalid profile fails open

- **WHEN** the resolved profile is missing or fails validation
- **THEN** the tool returns a structured "unresolved" result carrying the errors, not a hard error

#### Scenario: Archetype entries passed through opaquely

- **WHEN** a profile's archetype entry carries fields beyond a single model/effort/window scalar
- **THEN** the returned body preserves the entry verbatim without collapsing it

