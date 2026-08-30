## ADDED Requirements

### Requirement: On-demand skill body read tool

The server SHALL expose a tool letting a caller fetch a single skill's full `SKILL.md` body by name, so a worker that received only the lightweight name+description catalog up front (see skill-provisioning) can pull in a specific skill's full content once it decides it needs it. The tool SHALL require a non-empty `name` argument and MUST return a tool error naming the skill as not found when `name` does not match any entry in the catalog the worker was given.

#### Scenario: Known skill name returns its full body

- **WHEN** the tool is called with a `name` matching a catalog entry
- **THEN** the result contains that skill's full `SKILL.md` body content

#### Scenario: Missing name argument rejected

- **WHEN** the tool is called without a `name` argument, or with one that is empty after trimming
- **THEN** the response is a tool error reporting name is required

#### Scenario: Unknown skill name errors

- **WHEN** the tool is called with a `name` that does not match any known skill
- **THEN** the response is a tool error naming the skill as not found
