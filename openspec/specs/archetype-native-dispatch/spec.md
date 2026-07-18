# archetype-native-dispatch Specification

## Purpose
TBD - created by archiving change add-archetype-native-dispatch. Update Purpose after archive.
## Requirements
### Requirement: Archetype-based model resolution for native sub-agent dispatch

The system SHALL provide a documented convention, independent of hera worker spawn, by which a coordinator or pipeline dispatching Claude's native sub-agent tool for archetype-mapped stages (e.g. a migration stage mapped to `code_slice`, a review stage mapped to `review`, a CI-fix loop mapped to `ci_loop`) resolves each stage's model from the project's bound diligence profile via `mcp__argus__profile_resolve`, requiring no hera worker-spawn plumbing (no task, worktree, or branch).

#### Scenario: Native dispatch resolves the same profile hera spawn would

- **WHEN** a coordinator calls `profile_resolve(cwd=$PWD)` from within an argus task, hera-bound or not
- **THEN** it receives the same project-bound (or per-spawn-overridden) profile that would drive a hera worker spawned from the same task

#### Scenario: No hera session required

- **WHEN** a session with no live hera binding dispatches native sub-agents mapped to archetypes
- **THEN** archetype resolution still succeeds via `profile_resolve`'s cwd-based project lookup

### Requirement: Single resolution call per pipeline

The convention SHALL resolve the profile once per pipeline (or session) and build an archetype→`{model, effort}` map from that single response, rather than issuing one `profile_resolve` call per stage — `profile_resolve` already returns every archetype's entry in one response.

#### Scenario: Multi-stage pipeline resolves once

- **WHEN** a pipeline dispatches native sub-agents for three different archetypes (e.g. `code_slice`, `review`, `ci_loop`)
- **THEN** `profile_resolve` is called exactly once for the pipeline, and all three stages' models are read from that single response

### Requirement: Fail-open fallback for native dispatch

When `profile_resolve` returns `resolved: false`, or the resolved profile has no entry (or an empty `model`) for the specific archetype a stage maps to, the convention SHALL omit any model override for that stage and let the dispatch mechanism use its own default, never treating the miss as an error.

#### Scenario: Unresolved profile falls back entirely

- **WHEN** `profile_resolve` returns `resolved: false` for the calling project
- **THEN** every pipeline stage dispatches with no model override, using the caller's default model

#### Scenario: A single archetype missing from an otherwise-resolved profile

- **WHEN** a resolved profile has no `[archetype.docs]` table (or an empty `model` field within it)
- **THEN** only the `docs`-mapped stage falls back to the caller's default model; other stages whose archetypes ARE present in the profile still receive their resolved model

### Requirement: In-session model gate before native dispatch

Before threading a resolved archetype model into a native sub-agent call, the convention SHALL verify the model is one of the four values Claude's native sub-agent dispatch accepts in-session (`opus`, `sonnet`, `haiku`, `fable` — mirroring `internal/review.knownInSessionModels`). Native sub-agent dispatch has no path to any other backend at all, so a resolved model naming anything else (e.g. a foreign backend's model, since a profile's archetype model is validated against the union of every configured backend, not just Claude) SHALL NOT be forwarded as-is; instead the convention SHALL substitute the closest available in-session Claude model (a best-effort capability-tier mapping, not a validated cross-vendor equivalence) and note the substitution rather than silently dropping model selection or passing the foreign value through.

#### Scenario: In-session model forwarded

- **WHEN** an archetype resolves to `model = "sonnet"`
- **THEN** the native sub-agent call for that stage is dispatched with `model = "sonnet"`

#### Scenario: Foreign backend model substituted with the closest in-session tier

- **WHEN** an archetype resolves to a model belonging to a configured non-Claude backend (e.g. a codex model name)
- **THEN** the native sub-agent call for that stage dispatches with the closest available in-session Claude model substituted in its place, and the substitution is noted rather than silently ignored or left unresolved

### Requirement: Effort threaded only where the dispatch mechanism supports it

The convention SHALL thread an archetype's resolved `effort` into the dispatch call only when the dispatch mechanism accepts an effort parameter. When dispatching via a mechanism with no effort parameter, effort SHALL be omitted and the limitation SHALL be documented rather than silently unapplied or assumed to work.

#### Scenario: Effort threaded through a mechanism that accepts it

- **WHEN** an archetype resolves to `effort = "high"` and the dispatch mechanism exposes an effort parameter
- **THEN** the resolved effort is passed to that parameter

#### Scenario: Effort omitted where unsupported

- **WHEN** an archetype resolves to a non-empty `effort` value but the dispatch mechanism has no effort parameter
- **THEN** the stage dispatches with no effort override, and this limitation is documented rather than presented as effort having been applied

### Requirement: Reusable skill as the authoritative reference

The system SHALL ship one dedicated, `Skill()`-loadable reference documenting this convention (resolution timing, the fail-open rules, the in-session model gate, and the effort-threading scope), so that other skills and coordinators reference a single authoritative copy instead of re-deriving the same rules ad hoc.

#### Scenario: A pipeline skill defers to the shared convention

- **WHEN** a coordinator or pipeline skill needs to dispatch native sub-agents mapped to archetypes
- **THEN** it loads the shared skill for the resolution/fallback/gate rules rather than re-implementing them inline

