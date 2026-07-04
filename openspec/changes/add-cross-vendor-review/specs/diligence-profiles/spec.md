# diligence-profiles (delta)

## ADDED Requirements

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

## MODIFIED Requirements

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
