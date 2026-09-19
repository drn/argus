## ADDED Requirements

### Requirement: Cross-tab arrow navigation preference persists

The Appearance category SHALL provide a boolean row for Cmd+Left/Right navigation across the Tasks–Hera boundary. The option SHALL default to disabled when no stored value exists, SHALL display its effective enabled state, and SHALL persist changes to config key `ui.cross_tab_arrows` immediately when toggled with Right or Enter. Left SHALL continue to return focus from the pane to the Settings rail without toggling the option.

#### Scenario: Preference defaults to disabled

- **WHEN** no `ui.cross_tab_arrows` value is stored
- **THEN** the Appearance row shows cross-tab arrow navigation as disabled

#### Scenario: Enabling the preference persists it

- **WHEN** the cross-tab arrow row is selected and the user presses Right or Enter while it is disabled
- **THEN** the row becomes enabled and `ui.cross_tab_arrows` is written as `true`

#### Scenario: Left returns to the Settings rail

- **WHEN** the cross-tab arrow row is selected in the focused Settings pane and the user presses Left
- **THEN** focus returns to the Settings rail and the preference value does not change
