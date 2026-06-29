# Diligence Profiles

## ADDED Requirements

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

The system SHALL validate that a profile conforms: every archetype table names a canonical archetype;
each `effort` is one of `low`/`medium`/`high`; each `window` is one of `200k`/`1m`; every `model` is a
member of the union of built-in backend aliases and every configured backend's `models` list; the
`extends` chain terminates without a cycle; and any `[panel]` table is structurally well-formed.
Validation SHALL report all conformance errors found, not just the first.

#### Scenario: Unknown archetype rejected

- **WHEN** a profile declares `[archetype.planner]` (not a canonical archetype)
- **THEN** validation reports an unknown-archetype error

#### Scenario: Out-of-enum effort or window rejected

- **WHEN** a profile sets `effort = "max"` or `window = "2m"`
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

### Requirement: Reviewer-panel forward-reference seam

The `[panel]` table SHALL be retained verbatim by the loader as an opaque block whose composition
grammar is owned by a separate capability. This capability SHALL NOT define, constrain, or interpret the
panel's model/lens roster or synthesizer contract; it SHALL only confirm the block's structural shape.

#### Scenario: Panel block retained without interpretation

- **WHEN** a profile is loaded with a `[panel]` table
- **THEN** the panel block is available to consumers verbatim and is not otherwise interpreted by this
  capability

### Requirement: Profile-aware model resolution

The system SHALL resolve a task's effective model with the precedence: the per-task model override when
set; otherwise, when the task carries an archetype and the project's bound profile is present and valid,
the profile's model for that archetype; otherwise the project/backend default. When the bound profile is
missing or invalid, or the resolved profile model is not valid for the task's resolved backend, the
system SHALL resolve to **no model** (no `--model` injected) rather than failing the spawn. A task that
carries no archetype SHALL NOT consult any profile.

#### Scenario: Task override wins

- **WHEN** a task sets `Model = "opus"` and also has an archetype with a bound profile
- **THEN** the effective model is `opus` and the profile is not consulted

#### Scenario: Profile model applied by archetype

- **WHEN** a task has no model override, carries archetype `code_slice`, and the bound profile is valid
- **THEN** the effective model is the profile's `code_slice` model

#### Scenario: Invalid-for-backend model falls through

- **WHEN** the profile's archetype model is not valid for the task's resolved backend
- **THEN** resolution falls through to the project/backend default rather than injecting an invalid model

#### Scenario: Missing or invalid profile falls open

- **WHEN** the project's bound profile is missing or fails validation
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
conformance error or confirms the profile is valid. The affordance SHALL be documentation/operator
tooling only and SHALL NOT be wired into the Go build, CI, or any Make gate.

#### Scenario: Valid profile reported

- **WHEN** `validate` runs against a conforming profile
- **THEN** it reports the profile valid and names the source it resolved from

#### Scenario: Invalid profile reports all errors

- **WHEN** `validate` runs against a profile with multiple conformance errors
- **THEN** it reports each error and exits non-zero

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
