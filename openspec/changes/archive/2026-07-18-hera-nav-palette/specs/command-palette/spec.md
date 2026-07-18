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

The palette SHALL populate its row list as the union of: (1) the focused element's own applicable actions, (2) the CURRENT TAB's own rail/list action set (never a different tab's rail-scoped actions), and (3) the global action set — uniformly, without narrowing it by focus-based accidental-input guards that apply only to raw keypresses (a palette pick is a deliberate, filtered, Enter-confirmed action, not an incidental keystroke). Each row SHALL show the action's label and its currently-resolved key. The one exception preserving its pre-existing boundary is the classic fullscreen agent view, where the global action set stays gated off exactly as it already is for ordinary keypresses (that boundary predates this change and was never in question).

#### Scenario: Row list reflects the invoking context

- **WHEN** the palette is opened from the classic agent view
- **THEN** its rows are drawn from the agent view's own applicable action set only (no global actions, matching the existing `modeAgent` gating)

#### Scenario: Hera pane focus unions the focused pane's own actions with the Hera tab's rail — never another tab's rail

- **WHEN** the palette is opened while a Hera coordinator or worker terminal pane holds focus
- **THEN** its rows include that pane's own focused-element actions plus the Hera rail's action set (acting on the rail's current selection), and do NOT include the plain task list's or Settings' action sets

#### Scenario: Task-list and Settings palettes stay scoped to their own tab's rail

- **WHEN** the palette is opened from the plain task list, or from the Settings tab
- **THEN** its rows include that tab's own action set plus the global action set, and do NOT include the Hera rail's action set

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

### Requirement: Keymap metadata is the primary data source, with a narrow enumerated exception for two Hera pane literal actions

The palette SHALL source the bulk of its rows from the `keymap` package's own action metadata (`defaultSpecs`/`actionLabels`/`contextOrder`) — the same metadata that generates the `?` help overlay — so a config-driven rebind is reflected in the palette automatically, with no separate list to maintain. A narrow, explicitly enumerated exception exists for exactly two Hera pane-focus actions that are NOT part of the rebindable keymap system (the Ctrl+Z fullscreen toggle and the Ctrl+Y clipboard copy, both hardcoded literal `tcell.Key` cases in the Hera page): these two SHALL appear as fixed rows (their own hardcoded label and literal key) whenever a Hera terminal pane is the focused element, per the applicable-context requirement above. The palette SHALL NOT list any OTHER non-keymap action beyond this pair (e.g. the Cmd/Ctrl+Alt+arrow focus-ladder navigation stays out of scope — pure navigation, not a palette-worthy action).

#### Scenario: A rebind is reflected without code changes

- **WHEN** a `[keybindings.<context>]` override changes an action's bound key
- **THEN** the palette's row for that action shows the new key, matching the `?` help overlay

#### Scenario: Fullscreen and copy appear as fixed rows when a Hera pane is focused

- **WHEN** the palette is opened while a Hera coordinator or worker terminal pane holds focus
- **THEN** it lists a "toggle fullscreen" row and a "copy staged clipboard" row (each with their hardcoded key, `ctrl+z` and `ctrl+y`) alongside the keymap-sourced Hera rail rows

#### Scenario: Copy is absent from the coordinator Details/plan region

- **WHEN** the palette is opened while a coordinator's Details/plan region holds focus (not a terminal pane)
- **THEN** it lists the "toggle fullscreen" row but NOT the "copy staged clipboard" row (there is nothing to copy from a non-terminal region)

#### Scenario: No other non-keymap actions appear

- **WHEN** the palette is open in any context
- **THEN** it lists no action beyond keymap-sourced rows and the two enumerated Hera literal-action rows — no focus-ladder navigation entries, no other hardcoded literal appears
