## ADDED Requirements

### Requirement: Sandbox override selector on the new-task prompt

The new-task form SHALL present a **Sandbox** cycling selector alongside the
existing Backend/Model/Profile/Archetype selectors, offering exactly three
positions: `Inherit`, `Enabled`, `Disabled`, defaulting to `Inherit`. The
submitted task SHALL carry no sandbox override when the selector is left on
`Inherit`, a force-enabled override when cycled to `Enabled`, and a
force-disabled override when cycled to `Disabled`. The selector SHALL
participate in the form's Tab/Backtab and Up/Down focus order alongside the
other selector fields.

#### Scenario: Selector present and defaults to Inherit

- **WHEN** the new-task form is opened
- **THEN** it shows a Sandbox selector positioned on `Inherit`

#### Scenario: Leaving the selector on Inherit submits no override

- **WHEN** the user submits the form without touching the Sandbox selector
- **THEN** the produced task carries no sandbox override, so resolution falls back to the project/global setting

#### Scenario: Cycling to Enabled submits a force-enabled override

- **WHEN** the user cycles the Sandbox selector to `Enabled` and submits
- **THEN** the produced task carries a force-enabled sandbox override

#### Scenario: Cycling to Disabled submits a force-disabled override

- **WHEN** the user cycles the Sandbox selector to `Disabled` and submits
- **THEN** the produced task carries a force-disabled sandbox override
