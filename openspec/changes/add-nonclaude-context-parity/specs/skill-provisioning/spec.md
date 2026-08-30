## ADDED Requirements

### Requirement: Plain-text skill catalog for prompt embedding

The system SHALL provide a function that renders a skill catalog as plain text suitable for direct embedding in a prompt: one entry per skill, each carrying the skill's name and its one-line frontmatter description, reusing the existing `SkillItem` data rather than introducing a second metadata format or a second frontmatter parse path. When the input skill set is empty, the function SHALL produce output that its caller can omit cleanly (e.g. an empty string), not a "no skills found" placeholder line.

#### Scenario: Catalog renders name and description per skill

- **WHEN** the catalog is rendered from a non-empty set of `SkillItem` values
- **THEN** the output contains one entry per skill showing that skill's name and one-line description, and does not include any skill's full `SKILL.md` body

#### Scenario: Empty skill set produces omittable output

- **WHEN** the catalog is rendered from an empty set of `SkillItem` values
- **THEN** the output allows the caller to omit the skill catalog section entirely, rather than emitting a placeholder entry

### Requirement: Full skill body lookup by name

The system SHALL provide a function that resolves a single skill's full `SKILL.md` body content given the exact name it was exposed under in the catalog, searching the same sources the catalog was built from (builtin skills at minimum). Resolving a name that does not match any known skill SHALL return a distinct not-found outcome rather than an empty body.

#### Scenario: Known skill name resolves to its full body

- **WHEN** the lookup is called with a name matching a catalog entry
- **THEN** the result is that skill's full `SKILL.md` body content

#### Scenario: Unknown skill name is reported distinctly

- **WHEN** the lookup is called with a name that does not match any catalog entry
- **THEN** the result is a distinct not-found outcome, not an empty body and not a generic error indistinguishable from other failures
