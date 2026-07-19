## MODIFIED Requirements

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
