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

A profile SHALL describe, per archetype, optional backend-keyed model choices, `effort`, and `window`, plus a `[rigor]` table and an opaque `[panel]` table. A model choice SHALL be declared under the archetype's `models` table, keyed by backend name. The system SHALL recognize exactly thirteen canonical archetypes.

The `[rigor]` table SHALL retain `review_passes`, `gating`, and `security_spot_check`.

#### Scenario: Backend-keyed archetype models parse

- **WHEN** a profile declares `[archetype.code_slice.models]` with `claude = "sonnet"` and `codex = "gpt-5-codex"`
- **THEN** the loaded profile exposes both backend-specific choices for `code_slice`

#### Scenario: Rigor flags parse

- **WHEN** a profile declares `[rigor]` with its supported flags
- **THEN** the loaded profile exposes those flags

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

The system SHALL validate every backend-keyed archetype model against the selectable models for that named backend. A configured backend's `models` list SHALL take precedence; otherwise, its known CLI aliases SHALL be used. The system SHALL report an unknown backend key or a model unsupported by its named backend as a conformance error, alongside all other profile conformance errors.

It SHALL also reject unknown archetypes, invalid effort/window values, inheritance cycles, and malformed panels when a panel validator is injected, reporting all conformance errors rather than only the first.

#### Scenario: Model rejected for its named backend

- **WHEN** a profile declares `codex = "opus"` for an archetype
- **THEN** validation reports that `opus` is unsupported for the `codex` backend

#### Scenario: Model accepted for its named backend

- **WHEN** a profile declares `codex = "gpt-5-codex"` for an archetype
- **THEN** validation accepts that entry when Codex exposes that model

#### Scenario: Other validation failures remain reported

- **WHEN** a profile has an unknown archetype, invalid effort/window, an inheritance cycle, or an invalid panel
- **THEN** validation reports the relevant conformance error

### Requirement: Reviewer-panel forward-reference seam

The `[panel]` table SHALL be retained verbatim by the loader as a block whose composition grammar is defined by the `cross-vendor-review` capability. The `diligence-profiles` capability SHALL NOT hard-code the panel's grammar; instead it SHALL accept an injected panel-grammar validator (mirroring the injected known-models function) and apply it during validation when supplied, so that panel semantics live with the owning capability and no `profiles → review` import dependency is introduced.

#### Scenario: Panel block retained for consumers

- **WHEN** a profile is loaded with a `[panel]` table
- **THEN** the panel block is available to consumers verbatim

#### Scenario: Grammar enforced only via injection

- **WHEN** the panel-grammar validator is injected
- **THEN** panel conformance is enforced by that validator, and the profiles package itself does not import the review capability

### Requirement: Profile-aware model resolution

The system SHALL resolve a task's effective model with the precedence: a valid per-task model override; otherwise, the resolved profile's model for the task's selected backend and archetype; otherwise the selected backend's configured default; otherwise no injected model flag. An invalid explicit override or missing/invalid backend-specific profile entry SHALL fall through to the selected backend's configured default rather than reaching the backend CLI.

The profile consulted SHALL be the task's per-spawn override when present, otherwise its project binding, otherwise `default`; a task without an archetype SHALL not consult a profile.

#### Scenario: Backend-specific profile model applied

- **WHEN** a Codex task has no valid override, carries `code_slice`, and its valid profile declares `codex = "gpt-5-codex"`
- **THEN** the effective model is `gpt-5-codex`

#### Scenario: Unsupported explicit override falls through

- **WHEN** a Codex task sets `Model = "opus"`
- **THEN** the system does not inject `opus` and instead uses the Codex backend default or no model flag

#### Scenario: Profile selection and no-archetype behavior remain unchanged

- **WHEN** a task has a per-spawn profile override
- **THEN** it takes precedence over the project binding

- **WHEN** a task carries no archetype
- **THEN** it does not consult a profile

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
high-volume roles), with Fable treated as absent. These SHALL be embedded into the binary (not read from
a git checkout at runtime), so installing them works identically for a from-source build and a
release-binary install. The system SHALL expose a programmatic `InstallDefaults` affordance that writes
each seed into the per-user profile library when — and only when — no file already exists at that
destination; an existing file SHALL be left untouched (never overwritten) and reported as skipped rather
than silently ignored. Installation SHALL remain an explicit, user-triggered action — the system SHALL
NOT auto-write seeds on daemon startup or any other unattended path.

#### Scenario: Default seed covers all archetypes

- **WHEN** the `default` seed profile is validated
- **THEN** it conforms and provides a model for each canonical archetype

#### Scenario: Lean and customer_grade extend default

- **WHEN** the `lean` and `customer_grade` seed profiles are validated
- **THEN** they conform and express their differences as overrides of `default`

#### Scenario: Embedded seeds validate independently of a git checkout

- **WHEN** a seed profile's embedded bytes are extracted and loaded (no reliance on any file outside the
  binary's embedded data)
- **THEN** it passes `profiles.Validate` the same as when read from a source checkout

#### Scenario: Installing into an empty library writes every seed

- **WHEN** `InstallDefaults` runs against a profiles directory containing none of the seed names
- **THEN** every seed is written and reported as installed

#### Scenario: An existing file is never overwritten

- **WHEN** `InstallDefaults` runs and a seed name already exists at the destination
- **THEN** the existing file's bytes are left unmodified and its name is reported as skipped, not
  installed

#### Scenario: Installation is never automatic

- **WHEN** the daemon starts with no `~/.argus/profiles/` directory present
- **THEN** no seed files are written unless an operator explicitly triggers the install action

### Requirement: Agent-facing profile resolution

The system SHALL expose an `mcp__argus__profile_resolve` MCP tool that resolves the diligence profile in effect for a caller and returns the fully-resolved profile body as structured JSON. Resolution SHALL run daemon-side (the daemon can read `~/.argus/profiles`, which a sandboxed agent cannot) and SHALL reuse the existing `internal/profiles` loading, in-repo precedence, `extends` overlay, and validation rather than re-implementing them. The returned body SHALL carry the per-archetype entries, the `[rigor]` block, and the `[panel]` block, with per-archetype entries passed through verbatim (not collapsed to single scalars) so a future per-archetype model-menu extension does not break the contract. The JSON field names for archetype entries (`model`, `effort`, `window`) and the `[rigor]` block (`review_passes`, `gating`, `security_spot_check`) SHALL be lowercase/snake_case, matching the TOML keys a profile author writes — not the Go struct field names. Resolution SHALL fail open: a missing or invalid profile returns a structured "unresolved" result carrying the validation errors, never a hard tool error.

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

#### Scenario: Archetype and rigor JSON keys are lowercase

- **WHEN** `profile_resolve` returns a resolved profile whose `code_slice` archetype sets `model = "sonnet"` and whose `[rigor]` sets `review_passes = 2`
- **THEN** the raw JSON response contains the keys `"model"` and `"review_passes"` (not `"Model"` or `"ReviewPasses"`)
