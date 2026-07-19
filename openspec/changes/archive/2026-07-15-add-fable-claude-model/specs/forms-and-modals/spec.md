## MODIFIED Requirements

### Requirement: New-task model selection

The new-task form SHALL present the model field as a per-backend cycling selector rather than a raw text input. The selector's options SHALL be, in order: a `default` option (meaning "use the selected backend's configured default model, or the CLI's own default"), followed by the selected backend's known models, followed by a `custom…` option. Left/right SHALL cycle the selector value; up/down SHALL move field focus (unchanged from the other selectors). The `default` option SHALL be selected initially.

The selector's per-backend known-model list SHALL be resolved from the backend's configured `models` list when non-empty, otherwise from a built-in curated list keyed on the backend command (Claude backends supply the stable `claude` CLI aliases `opus`, `sonnet`, `haiku`, `fable`; Codex backends supply the Codex model identifiers; unknown, Pi, and custom backends supply an empty list — leaving only `default` and `custom…`).

When the backend selector changes, the model selector's option list SHALL be rebuilt for the newly selected backend and the selection SHALL reset to `default`, and any previously typed custom value SHALL be cleared.

When the `custom…` option is selected, the form SHALL reveal a single-line text input for a model identifier the built-in/configured list does not contain, and the typed value SHALL be used verbatim as the task's model.

The submitted task's model value SHALL be: an empty string when `default` is selected; the chosen model string when a listed model is selected; the trimmed typed text when `custom…` is selected. This preserves the existing semantics where an empty model defers to the backend default / CLI default and a non-empty model is injected as `--model`.

#### Scenario: Default selection yields no model override

- **WHEN** the model selector is left on `default` and the form is submitted
- **THEN** the produced task carries an empty model value (the backend default / CLI default applies)

#### Scenario: Selecting a listed model

- **WHEN** a Claude backend is selected and the user cycles the model selector to `sonnet` and submits
- **THEN** the produced task carries `sonnet` as its model value

#### Scenario: Custom model fallback

- **WHEN** the user cycles the model selector to `custom…`, types a model identifier not in the list, and submits
- **THEN** the produced task carries the trimmed typed identifier as its model value
