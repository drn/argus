## ADDED Requirements

### Requirement: Web new-task sandbox override select

The web new-task form SHALL present a Sandbox override `<select>` alongside
the existing Backend and Model fields, offering exactly three options:
`Inherit` (value `""`), `Enabled` (value `"enabled"`), and `Disabled` (value
`"disabled"`), defaulting to `Inherit`. The selected value SHALL be submitted
as `sandbox_override` in the create-task request body.

#### Scenario: Sandbox select present and defaults to Inherit

- **WHEN** the user opens the web new-task form
- **THEN** it shows a Sandbox select defaulted to `Inherit`

#### Scenario: Inherit submits no override

- **WHEN** the user submits the form without changing the Sandbox select
- **THEN** the create request carries an empty `sandbox_override`

#### Scenario: Enabled or Disabled submits that override

- **WHEN** the user selects `Enabled` or `Disabled` and submits
- **THEN** the create request carries `sandbox_override` set to `"enabled"` or `"disabled"` respectively
