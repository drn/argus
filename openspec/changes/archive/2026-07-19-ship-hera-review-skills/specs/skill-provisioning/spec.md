## ADDED Requirements

### Requirement: Builtin skill body bundle

The system SHALL embed argus-coupled skill bodies — skills that drive `mcp__argus__*` tools, read `ARGUS_TASK_ID`/`~/.argus`, or encode the hera coordination model — directly into the argus binary via `go:embed`, as one `SKILL.md` per subdirectory under the embedded root. The embedded set SHALL be derived generically by iterating the embedded directory tree (no hardcoded per-skill name list), so adding a new builtin skill requires only adding its directory, not a code change.

#### Scenario: Embedded set includes the review skills

- **WHEN** the embedded builtin skill set is enumerated
- **THEN** it includes `hera-review` and `hera-review-test-adversary` alongside the pre-existing `archive`, `argus-complete`, `argus-schedule`, `hera`, and `hera-plan` entries

### Requirement: Idempotent materialization for --add-dir delivery

The system SHALL materialize the embedded skill bodies to `~/.argus/skills/.claude/skills/<name>/`, rewriting only files whose content differs from what is already on disk, and SHALL remove materialized skill directories that no longer correspond to an embedded skill. The materializing function SHALL return the workspace root path on success, suitable for direct use as a `--add-dir` argument, and SHALL be inert (no filesystem writes, empty path, no error) when running inside a Go test binary.

#### Scenario: Materialization is inert during automated tests

- **WHEN** the materializing function runs inside a Go test binary
- **THEN** it returns an empty path and no error, performing no filesystem writes under `~/.argus/`
