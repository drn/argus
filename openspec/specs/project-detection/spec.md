# Project Detection & Skills

## Purpose

This capability gives the UI two pieces of contextual metadata about a working directory and the user's tooling. First, it inspects a project directory for well-known build-system marker files and reports a Nerd Font icon plus a language name so the task list and project views can label projects at a glance. Second, it discovers the Claude Code skills and slash commands available on the machine — from project-scoped directories, the user's `~/.claude/skills`, and installed plugins — so the new-task form can offer them for autocomplete.

## Requirements

### Requirement: Detect project language from marker files

The capability SHALL inspect a directory for a fixed set of well-known build-system marker files and return the language name associated with the first matching marker. When no marker is found it SHALL return an empty language name.

#### Scenario: Recognised marker present

- **WHEN** a directory contains a recognised marker file (for example `go.mod`, `Cargo.toml`, `package.json`, `tsconfig.json`, `Gemfile`, `requirements.txt`, `setup.py`, `pyproject.toml`, `pom.xml`, `build.gradle`, or `mix.exs`)
- **THEN** the detected language is the name mapped to that marker (`go`, `rust`, `node`, `typescript`, `ruby`, `python`, `python`, `python`, `java`, `java`, `elixir` respectively)

#### Scenario: No recognised marker present

- **WHEN** a directory contains none of the recognised marker files
- **THEN** the detected language is the empty string

### Requirement: Detect project icon from marker files

The capability SHALL return a Nerd Font icon for a directory based on the first matching marker file, and SHALL return a generic folder icon when no marker matches. Some markers (such as `Makefile` and `.git`) map to an icon but carry no language name.

#### Scenario: Language marker yields its icon

- **WHEN** a directory contains a recognised language marker such as `go.mod`
- **THEN** the returned icon is the marker's mapped icon, not the generic folder fallback

#### Scenario: Git directory yields a non-fallback icon

- **WHEN** a directory contains a `.git` directory and no higher-priority marker
- **THEN** the returned icon is the git icon, not the generic folder fallback

#### Scenario: No marker yields the folder fallback

- **WHEN** a directory contains none of the recognised marker files
- **THEN** the returned icon is the generic folder fallback icon

### Requirement: First-match marker priority

When a directory contains more than one recognised marker file, the capability SHALL resolve detection to the first marker in a fixed priority order, so detection is deterministic and independent of filesystem listing order.

#### Scenario: Higher-priority marker wins

- **WHEN** a directory contains both `go.mod` and `package.json`
- **THEN** the detected language is `go`, because `go.mod` precedes `package.json` in the priority order

### Requirement: Discover skills from project and user directories

The capability SHALL discover skills by scanning each supplied project-scoped directory and then the user's `~/.claude/skills` directory, treating each immediate sub-directory that contains a `SKILL.md` manifest as one skill whose name is the sub-directory name. Non-directory entries SHALL be ignored, and a directory that cannot be read SHALL contribute no skills rather than causing an error.

#### Scenario: User skill is discovered

- **WHEN** `~/.claude/skills/<name>/SKILL.md` exists
- **THEN** the discovered skill list includes an item named `<name>`

#### Scenario: Missing skills directory is tolerated

- **WHEN** a scanned skills directory does not exist or cannot be read
- **THEN** no skills are contributed from that directory and no error is surfaced

### Requirement: Discover plugin commands and skills

The capability SHALL read `~/.claude/plugins/installed_plugins.json` and, for each installed plugin that has an install path, expose its `commands/*.md` files and any `SKILL.md` found recursively under `skills/` as items named `<plugin>:<name>`. Command names SHALL be the file base name without the `.md` extension; plugin-skill names SHALL be the manifest `name` field, falling back to the skill's directory name when that field is absent. Non-`.md` files in the commands directory SHALL be ignored. When the manifest is missing or cannot be parsed, plugin discovery SHALL contribute nothing while other sources still load.

#### Scenario: Plugin command and skill are namespaced

- **WHEN** an installed plugin `cortex` ships `commands/review.md` and a `skills/.../SKILL.md` whose frontmatter `name` is `font-licensing`
- **THEN** the discovered list includes `cortex:review` and `cortex:font-licensing`

#### Scenario: Non-markdown command files are ignored

- **WHEN** a plugin's `commands` directory contains a non-`.md` file alongside `.md` command files
- **THEN** only the `.md` command files become items

#### Scenario: Missing plugin manifest does not block user skills

- **WHEN** `installed_plugins.json` is absent but a user skill exists under `~/.claude/skills`
- **THEN** the discovered list still contains the user skill and contains no plugin items

#### Scenario: Plugin skills directory is a symlink

- **WHEN** a plugin's `skills/` entry is a symlink to a real directory containing a `SKILL.md`
- **THEN** the symlink is followed and the contained skill is discovered

#### Scenario: Dangling plugin skills symlink is skipped

- **WHEN** a plugin's `skills/` entry is a symlink whose target does not exist, but its `commands/` directory is valid
- **THEN** no skill is discovered from the dangling symlink and the plugin's commands are still discovered

### Requirement: Reject unsafe plugin names

The capability SHALL skip any plugin whose name is empty or contains control characters or ANSI escape sequences (null, ESC, newline, carriage return, or tab), so that malicious manifest entries cannot inject characters into rendered names.

#### Scenario: Plugin name with control characters is rejected

- **WHEN** the manifest declares a plugin whose name contains an ANSI escape and a newline
- **THEN** that plugin contributes no items to the discovered list

### Requirement: Name-collision precedence

The capability SHALL keep the first item seen for any given name and drop later duplicates, scanning project directories first, then the user skills directory, then plugins, so project-scoped skills win over user skills with the same name.

#### Scenario: Project skill overrides user skill

- **WHEN** both a project-scoped directory and `~/.claude/skills` define a skill named `review` with different descriptions
- **THEN** the discovered list contains a single `review` item whose description is the project-scoped one

### Requirement: Deterministic sorted output

The capability SHALL return discovered skills sorted by name, so the same set of inputs always yields the same ordering.

#### Scenario: Mixed sources are returned sorted

- **WHEN** user skills and plugin items are discovered together
- **THEN** the returned list is ordered ascending by name

### Requirement: Read description from manifest frontmatter

The capability SHALL read each item's description from a single top-level field in the YAML-style frontmatter block fenced by `---` lines at the start of the manifest, stripping surrounding quotes. It SHALL return an empty value when the file cannot be opened, the field is absent, or a frontmatter line exceeds the maximum line size, rather than returning a truncated or garbled value.

#### Scenario: Description is parsed and unquoted

- **WHEN** a manifest frontmatter contains `description: "User commit skill"`
- **THEN** the item's description is `User commit skill` without the surrounding quotes

#### Scenario: Over-long frontmatter line yields empty description

- **WHEN** a manifest's description value exceeds the maximum frontmatter line size
- **THEN** the parsed description is the empty string

### Requirement: Filter skills by case-insensitive substring

The capability SHALL filter a list of skills to those whose name contains a given filter string as a case-insensitive substring, matching anywhere in the name. An empty filter SHALL return the full list unchanged.

#### Scenario: Substring matches user and plugin names

- **WHEN** the list contains `review` and `cortex:review` and the filter is `re`
- **THEN** both `review` and `cortex:review` are returned

#### Scenario: Empty filter returns all

- **WHEN** the filter string is empty
- **THEN** every item in the input list is returned

#### Scenario: Filter is case-insensitive

- **WHEN** the filter is `CO` and the list contains `commit` and `cortex:review`
- **THEN** both `commit` and `cortex:review` are returned
