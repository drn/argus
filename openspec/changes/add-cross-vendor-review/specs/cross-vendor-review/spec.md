# cross-vendor-review (delta)

## ADDED Requirements

### Requirement: Reviewer-panel composition grammar

This capability SHALL define and validate the `[panel]` block grammar consumed from a diligence profile, and SHALL supply that validator to `diligence-profiles` by injection (so the profiles package never imports this capability). A well-formed `[panel]` SHALL contain: a non-empty `finders` list of reviewer ids, each resolvable to a known in-session model or a configured backend; an optional list of lenses, each with a non-empty `name`, a known `model`, and an optional `skill`; an optional `synthesizer` naming a known model; an optional `review_skill` or `review_instruction` (at most one); and an optional boolean `fix_verification`.

#### Scenario: Well-formed panel accepted

- **WHEN** a `[panel]` declares a non-empty `finders` list of known reviewer ids
- **THEN** the panel-grammar validator accepts it

#### Scenario: Empty or unknown finders rejected

- **WHEN** a `[panel]` has an empty `finders` list or names a finder id that resolves to no known model or backend
- **THEN** the validator reports the offending `finders` error

#### Scenario: Malformed lens rejected

- **WHEN** a lens entry has an empty `name` or an unknown `model`
- **THEN** the validator reports the offending lens error

#### Scenario: Conflicting review instruction rejected

- **WHEN** a `[panel]` sets both `review_skill` and `review_instruction`
- **THEN** the validator reports the conflict

### Requirement: User-owned review instruction with a shipped default

The review instruction each broad finder runs SHALL be user-owned and selected from the profile's `[panel]` (`review_skill` names a skill, or `review_instruction` supplies prose). When neither is specified, the system SHALL inject the shipped default `hera-review` instruction. The orchestration glue SHALL NOT hard-code the review methodology; swapping the review instruction SHALL require no change to the glue or the synthesizer.

#### Scenario: Configured review skill injected

- **WHEN** a profile's `[panel]` sets `review_skill = "my-review"`
- **THEN** each broad finder is spawned with the `my-review` instruction injected

#### Scenario: Default instruction when unspecified

- **WHEN** a profile's `[panel]` sets neither `review_skill` nor `review_instruction`
- **THEN** finders run the shipped default `hera-review` instruction

#### Scenario: Prose instruction honored

- **WHEN** a profile's `[panel]` sets `review_instruction` prose
- **THEN** that prose is injected as the finder review instruction

### Requirement: Vendor-diverse panel composition

The orchestration SHALL compose the panel from the resolved profile's `[panel]`, running each broad finder over the full diff and each lens with its own instruction. Anthropic-family broad finders (e.g. `opus`, `fable`) and lens finders SHALL run as in-session sub-agents; a foreign finder (e.g. `codex`) SHALL run as a spawned session whose output is obtained via `foreign-reviewer-capture`. When no profile resolves, the orchestration SHALL fall back to a built-in default panel.

#### Scenario: Broad finders run over the full diff

- **WHEN** the panel lists `finders = ["opus", "fable", "codex"]`
- **THEN** each broad finder reviews the full diff (none is narrowed to a single lane)

#### Scenario: Foreign finder routed through capture

- **WHEN** a finder id resolves to a foreign backend (e.g. `codex`)
- **THEN** it is run as a spawned reviewer-mode session and its output is obtained via `foreign-reviewer-capture`

#### Scenario: Fallback panel without a profile

- **WHEN** no diligence profile resolves for the project
- **THEN** the orchestration composes a built-in default panel rather than failing

### Requirement: Cross-vendor synthesis contract

A single Anthropic synthesizer SHALL consolidate all finder outputs: normalize each into the canonical finding schema (`[AUTO-FIX]`/`[QUESTION]`/`[SPEC-DRIFT]`/`[ACKNOWLEDGED]`/`[SKIP]`); deduplicate while preserving provenance (which finders reported each issue); and assign confidence by cross-vendor corroboration. A foreign finder SHALL NOT make the final `[AUTO-FIX]` determination.

#### Scenario: Foreign output normalized into the schema

- **WHEN** a foreign finder emits free-form findings
- **THEN** the synthesizer normalizes them into the canonical tags rather than trusting the finder's own tags

#### Scenario: Deduplication preserves provenance

- **WHEN** the same issue is reported by two finders
- **THEN** the synthesizer emits a single finding recording both finders as its provenance

#### Scenario: Foreign never decides auto-fix

- **WHEN** a finding originates only from a foreign finder
- **THEN** the final `[AUTO-FIX]` determination is made by the Anthropic synthesizer, not the foreign finder

### Requirement: Single-finder adversarial gate

A finding corroborated by two or more independent finders MAY be eligible for `[AUTO-FIX]` directly. A finding reported by only one finder SHALL pass an adversarial verification pass (which defaults to "not real" under uncertainty) before it is eligible for `[AUTO-FIX]`; on refutation or uncertainty it SHALL be downgraded to `[QUESTION]` and surfaced, never silently dropped.

#### Scenario: Corroborated finding eligible directly

- **WHEN** a finding is reported by two or more independent finders
- **THEN** it is eligible for `[AUTO-FIX]` without the adversarial pass

#### Scenario: Single-finder finding gated

- **WHEN** a finding is reported by exactly one finder and fails or is uncertain under adversarial verification
- **THEN** it is downgraded to `[QUESTION]` and surfaced rather than auto-fixed

### Requirement: Fix-verification phase

The orchestration SHALL run a fix-verification phase distinct from per-area review that adversarially asks whether the fix works in the shipped artifact (reading deploy surfaces such as build scripts, entrypoint, config precedence, and test-suite integrity). The phase SHALL default to artifact reasoning without a build and MAY escalate to a real run via `iris_run_checks` where the project provides `script/iris-check`.

#### Scenario: Reasoning-default artifact check

- **WHEN** fix-verification runs and the project provides no `script/iris-check`
- **THEN** it performs adversarial artifact reasoning over the deploy surfaces without building

#### Scenario: Real-run escalation when supported

- **WHEN** the project provides `script/iris-check`
- **THEN** fix-verification MAY invoke `iris_run_checks` to exercise the shipped path

### Requirement: Fix-and-re-review loop

After applying accepted fixes, the orchestration SHALL re-review the result and repeat until no further auto-fixable findings remain, spawning fix-workers that follow the target project's own contribution conventions and report a confidence signal.

#### Scenario: Re-review after fixes

- **WHEN** accepted fixes have been applied in a review round
- **THEN** the orchestration re-reviews the updated code and terminates when no auto-fixable findings remain

#### Scenario: Fix-workers follow project conventions

- **WHEN** a fix-worker is spawned to apply a finding
- **THEN** it follows the target project's own conventions (e.g. spec-first and tests when the project requires them) and reports a confidence signal
