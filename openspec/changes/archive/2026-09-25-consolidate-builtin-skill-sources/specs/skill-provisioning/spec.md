## MODIFIED Requirements

### Requirement: Builtin skill body bundle

The system SHALL treat `internal/skills/builtin/<name>/SKILL.md` as the sole in-repo source for each argus-coupled builtin skill and SHALL embed those bodies directly into the argus binary via `go:embed`, one `SKILL.md` per subdirectory under the embedded root. The system SHALL NOT require or maintain a same-named builtin mirror under a repository's `.agents/skills/` or `.claude/skills/` tree. The embedded set SHALL be derived generically by iterating the embedded directory tree (no hardcoded per-skill name list), so adding a new builtin skill requires only adding its canonical directory, not a code change or project-local copy. Genuinely project-specific skills MAY remain under `.agents/skills/` and are not part of the embedded set.

#### Scenario: Embedded set includes every shipped builtin

- **WHEN** the embedded builtin skill set is enumerated
- **THEN** it includes `argus-archive`, `argus-complete`, `argus-recycle`, `argus-resolve-model`, `argus-schedule`, `hera`, `hera-plan`, `hera-review`, `hera-review-test-adversary`, and `hera-spawn-review`

#### Scenario: Builtin has no repository-local mirror

- **WHEN** an embedded builtin name also exists beneath the Argus repository's `.agents/skills/` or `.claude/skills/` directory
- **THEN** the source-layout regression test fails, requiring the project-local duplicate to be removed

#### Scenario: Project-only skill remains independent

- **WHEN** a repository-specific skill such as `update-agent-models` is not present under `internal/skills/builtin/`
- **THEN** it MAY remain under `.agents/skills/` and is not materialized as an Argus builtin
