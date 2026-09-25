## MODIFIED Requirements

### Requirement: User-owned review instruction with a shipped default

The review instruction each broad finder runs SHALL be user-owned and selected from the profile's `[panel]` (`review_skill` names a skill, or `review_instruction` supplies prose). When neither is specified, the system SHALL inject the shipped default `hera-review` instruction. Named review and corrective-lens skills SHALL be resolved by their exact, case-sensitive IDs from the current child session's native skill catalog, using the selected catalog entry's advertised `SKILL.md` path when direct loading is unavailable. Resolution SHALL use the current backend's native source precedence; a project-scoped same-ID definition is an intentional user override when that catalog selects it, and the orchestration SHALL NOT manually prefer a managed builtin or assume the current repository contains a project-skill mirror. A configured ID absent from the current catalog, an unresolved visible collision, or an unreadable selected manifest SHALL fail loudly before any finder or lens is spawned rather than silently falling back. The orchestration glue SHALL NOT hard-code the review methodology; swapping the review instruction SHALL require no change to the glue or the synthesizer.

#### Scenario: Configured review skill injected

- **WHEN** a profile's `[panel]` sets `review_skill = "my-review"`
- **THEN** each broad finder is spawned with the exact `my-review` instruction loaded from the current session's skill catalog

#### Scenario: Default instruction when unspecified

- **WHEN** a profile's `[panel]` sets neither `review_skill` nor `review_instruction`
- **THEN** finders run the shipped default `hera-review` instruction

#### Scenario: Prose instruction honored

- **WHEN** a profile's `[panel]` sets `review_instruction` prose
- **THEN** that prose is injected as the finder review instruction

#### Scenario: Builtin instructions resolve without a project mirror

- **WHEN** an Argus-launched panel needs the builtin `hera-review` or `hera-review-test-adversary` instruction and the current repository has no project-local skill directory
- **THEN** the instruction is loaded from the session-provisioned skill catalog and every applicable finder/lens receives it

#### Scenario: Project-scoped same-ID skill is an intentional override

- **WHEN** a profile names a skill ID that the current session's native catalog resolves to a project-scoped definition rather than the Argus-managed builtin
- **THEN** the finder or lens receives the project-scoped instruction selected by that catalog

#### Scenario: Missing or unresolved named instruction fails before spawning

- **WHEN** `review_skill` or a lens names an ID absent from the current session's catalog, exposes an unresolved collision, or names an unreadable selected manifest
- **THEN** orchestration stops with a loud lookup error before spawning any finder or lens, with no silent fallback
