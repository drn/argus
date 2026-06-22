# mobile-pwa

## ADDED Requirements

### Requirement: Web new-task model select

The web new-task form SHALL present the model field as a per-backend `<select>` rather than a free-text input. The select SHALL be populated from the backend roster the form already fetches, which SHALL include a per-backend `models` list. The options SHALL be, in order: a `default` option (labeled to surface the backend's configured default model when one exists), the backend's known models, and a `custom…` option that reveals a text input for a model not in the list. When the backend selector changes, the model select SHALL be repopulated for the newly selected backend and reset to `default`.

The value submitted with the create request SHALL be: empty when `default` is selected (so the backend/CLI default applies), the chosen model when a listed model is selected, or the trimmed typed value when `custom…` is selected — matching the value semantics of the prior free-text field.

#### Scenario: Model select populates from the selected backend

- **WHEN** the user opens the web new-task form and selects a backend
- **THEN** the model select offers that backend's `default`, its known models, and `custom…`

#### Scenario: Changing backend repopulates the model select

- **WHEN** the user changes the backend selector after choosing a model
- **THEN** the model select is repopulated for the new backend and reset to `default`

#### Scenario: Custom model via the web form

- **WHEN** the user selects `custom…` and types a model identifier
- **THEN** the create request carries that trimmed identifier as the model value
