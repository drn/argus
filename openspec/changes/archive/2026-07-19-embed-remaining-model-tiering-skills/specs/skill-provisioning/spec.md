## MODIFIED Requirements

### Requirement: Builtin skill body bundle

The system SHALL embed argus-coupled skill bodies — skills that drive `mcp__argus__*` tools, read `ARGUS_TASK_ID`/`~/.argus`, or encode the hera coordination model — directly into the argus binary via `go:embed`, as one `SKILL.md` per subdirectory under the embedded root. The embedded set SHALL be derived generically by iterating the embedded directory tree (no hardcoded per-skill name list), so adding a new builtin skill requires only adding its directory, not a code change.

#### Scenario: Embedded set includes the review-panel and archetype-model-resolution skills

- **WHEN** the embedded builtin skill set is enumerated
- **THEN** it includes `hera-spawn-review` and `resolve-archetype-model` alongside the pre-existing `archive`, `argus-complete`, `argus-schedule`, `hera`, `hera-plan`, `hera-review`, and `hera-review-test-adversary` entries
