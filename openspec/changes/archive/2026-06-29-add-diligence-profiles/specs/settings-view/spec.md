# Settings View

## ADDED Requirements

### Requirement: Project profile selection from validated on-disk profiles

The Settings project view SHALL present the project's bound profile as a select-list populated from the
profiles discoverable on disk (the per-user library and any in-repo directory). Only profiles that pass
validation SHALL be selectable; invalid profiles SHALL be shown as non-selectable (or excluded) so the
operator cannot bind a project to a malformed profile. Selecting a profile SHALL persist its name on the
project; the profile body SHALL NOT be persisted.

#### Scenario: Valid profiles are offered

- **WHEN** the project view is opened and the disk holds a mix of valid and invalid profiles
- **THEN** the select-list offers the valid profiles and the currently bound name

#### Scenario: Invalid profiles are not selectable

- **WHEN** a profile on disk fails validation
- **THEN** it cannot be chosen as a project's binding

#### Scenario: Selection persists the name only

- **WHEN** the operator selects a profile for a project
- **THEN** the project's stored binding is the profile name, and no profile body is written
