## ADDED Requirements

### Requirement: Embedded builtin skill set

Argus SHALL compile a fixed set of argus-coupled skills into the binary via
`go:embed`, sourced from `internal/skills/builtin/<name>/`, where each skill is a
directory containing a `SKILL.md` (and optionally supporting files). The embedded
set SHALL be the single source of truth for these skills — no runtime path,
network fetch, or external repository (e.g. `~/.dots`) is consulted to obtain
them. `BuiltinItems` SHALL return one entry per embedded skill, carrying the
skill's name (its directory name) and its `SKILL.md` `description` frontmatter
field.

#### Scenario: Embedded set enumerated from the binary

- **WHEN** `BuiltinItems` is called
- **THEN** it returns one entry per embedded skill directory, each with the directory name and the description parsed from that skill's `SKILL.md` frontmatter, with no filesystem or network access to any external source

### Requirement: Idempotent materialization to a managed directory

Argus SHALL materialize the embedded skills to a managed directory laid out as
`~/.argus/skills/.claude/skills/<name>/`, creating the `~/.argus/skills` root and
its nested `.claude/skills` directory if absent. `EnsureBuiltinSkills` SHALL
return the workspace root (`~/.argus/skills`) suitable for use as a Claude Code
`--add-dir` argument. Materialization SHALL be idempotent and content-gated: a
file SHALL be (re)written only when its on-disk content differs from the embedded
copy, and each write SHALL be atomic (temp file plus rename). The embedded copy
SHALL always win over a locally-modified on-disk copy. The managed
`.claude/skills` subtree SHALL mirror the embedded set exactly: skill directories
present on disk but absent from the embedded set SHALL be removed, and removal
SHALL be confined to the argus-managed `.claude/skills` subtree.

#### Scenario: First run creates the managed tree

- **WHEN** `EnsureBuiltinSkills` runs and `~/.argus/skills` does not exist
- **THEN** `~/.argus/skills/.claude/skills/<name>/SKILL.md` is created for every embedded skill, and the returned root is `~/.argus/skills`

#### Scenario: No rewrite when already current

- **WHEN** `EnsureBuiltinSkills` runs twice with an unchanged embedded set
- **THEN** the second run rewrites no file (materialized files' modification times are unchanged)

#### Scenario: Drifted or locally-edited file is restored

- **WHEN** a materialized `SKILL.md` has been edited on disk to differ from the embedded copy
- **THEN** the next `EnsureBuiltinSkills` run rewrites it to match the embedded content

#### Scenario: Stale skill directory pruned

- **WHEN** the managed `.claude/skills` contains a skill directory that is no longer in the embedded set
- **THEN** that directory is removed, while directories still in the embedded set are preserved

### Requirement: Builtin skills surfaced in the new-task picker

`LoadSkills` SHALL include the embedded argus skills in the skill list that
powers the new-task prompt autocomplete, so the picker lists skills the booted
task can actually invoke. Builtin skills SHALL be merged at the lowest
precedence: a skill of the same name discovered in a personal (`~/.claude/skills`),
project, or plugin location SHALL take precedence over the builtin entry and the
name SHALL appear at most once. This mirrors Claude Code's runtime discovery
precedence, under which a personal or project skill shadows a skill loaded from
an `--add-dir` directory.

#### Scenario: Builtin skill listed when not otherwise present

- **WHEN** `LoadSkills` runs and an embedded skill name is not present in any personal, project, or plugin location
- **THEN** the returned list includes that builtin skill exactly once

#### Scenario: Personal skill shadows the builtin of the same name

- **WHEN** a skill of the same name exists both in the embedded set and in `~/.claude/skills`
- **THEN** the returned list contains that name exactly once, sourced from the personal location
