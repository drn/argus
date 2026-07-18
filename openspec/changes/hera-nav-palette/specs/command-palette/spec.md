## ADDED Requirements

### Requirement: Global command palette invocation

The system SHALL provide a global command palette action (`global.palette`, default `ctrl+k`) that opens regardless of the current mode or focus region — the classic fullscreen agent view, the plain task list, and both Hera rail and Hera pane/coordinator focus. The action SHALL be resolved and dispatched ahead of the normal per-mode gated dispatch (the same class of guaranteed-global reach as the existing Ctrl+Q/Ctrl+Z structural interceptions), so no mode or focused pane can shadow it.

#### Scenario: Opens from the fullscreen agent view

- **WHEN** the user presses `ctrl+k` while a classic agent view is fullscreen (`modeAgent`)
- **THEN** the command palette opens over the agent view

#### Scenario: Opens from a live Hera pane, overriding PTY passthrough

- **WHEN** the user presses `ctrl+k` while a Hera coordinator or worker terminal pane holds focus
- **THEN** the command palette opens and the `ctrl+k` byte does NOT reach the pane's live PTY

#### Scenario: Opens from the plain task list

- **WHEN** the user presses `ctrl+k` while the task list holds focus (no agent view, no Hera pane)
- **THEN** the command palette opens

### Requirement: Palette lists actions applicable to the current context

The palette SHALL populate its row list from the keymap actions applicable to the region that held focus when it was opened (e.g. the classic agent view's action set, the task list's action set, or the Hera rail/pane action set), each row showing the action's label and its currently-resolved key.

#### Scenario: Row list reflects the invoking context

- **WHEN** the palette is opened from the classic agent view
- **THEN** its rows are drawn from the agent view's applicable action set, not the task list's or Hera's

#### Scenario: Each row shows the action's bound key

- **WHEN** the palette is open
- **THEN** every row displays both the action's human-readable label and the key chord currently bound to it

### Requirement: Type-to-filter narrows the row list

The system SHALL narrow the visible rows by substring match against the action label as the user types, and SHALL move the cursor with the arrow keys among the currently-visible rows.

#### Scenario: Typing narrows the list

- **WHEN** the user types a substring of an action's label
- **THEN** only rows whose label contains that substring (case-insensitive) remain visible

#### Scenario: Arrow keys navigate the filtered list

- **WHEN** the user presses Down/Up while rows are filtered
- **THEN** the cursor moves among the currently-visible filtered rows only

### Requirement: Enter invokes the selected action immediately

The system SHALL invoke the action under the cursor immediately on `Enter` — not merely display or copy it — and then close the palette. Invocation SHALL reuse the same underlying implementation the action's bound key already calls (no duplicated logic), and SHALL preserve whatever runtime guard that implementation has, so an inapplicable selection safely no-ops exactly as pressing the physical key would.

#### Scenario: Enter runs the action and closes the palette

- **WHEN** the user presses `Enter` with a row selected
- **THEN** the system performs that action's effect (the same effect its bound key would produce) and the palette closes

#### Scenario: An inapplicable action no-ops safely

- **WHEN** the user invokes, from the palette, an action whose runtime guard is not currently satisfied (e.g. a pane-switch action while side panels are collapsed)
- **THEN** the invocation is a safe no-op — identical to pressing the action's physical key in the same state — and does not crash or corrupt state

### Requirement: Keymap metadata is the single data source

The palette SHALL source its rows exclusively from the `keymap` package's own action metadata (`defaultSpecs`/`actionLabels`/`contextOrder`) — the same metadata that generates the `?` help overlay — so a config-driven rebind is reflected in the palette automatically, with no separate list to maintain. The palette SHALL NOT list actions that are not part of the rebindable keymap system (e.g. Hera's hardcoded, non-rebindable page-level keys such as the Ctrl+Z fullscreen toggle).

#### Scenario: A rebind is reflected without code changes

- **WHEN** a `[keybindings.<context>]` override changes an action's bound key
- **THEN** the palette's row for that action shows the new key, matching the `?` help overlay

#### Scenario: Non-keymap Hera literal keys are absent

- **WHEN** the palette is opened from a Hera pane
- **THEN** it does not list the Ctrl+Z fullscreen toggle or other hardcoded Hera page-level keys that are not modeled as keymap actions
