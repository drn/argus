## MODIFIED Requirements

### Requirement: Palette lists actions applicable to the current context

The palette SHALL populate its row list as the union of: (1) the focused element's own applicable actions, (2) the CURRENT TAB's own rail/list action set (never a different tab's rail-scoped actions), and (3) the global action set — uniformly, without narrowing it by focus-based accidental-input guards that apply only to raw keypresses (a palette pick is a deliberate, filtered, Enter-confirmed action, not an incidental keystroke). Each row SHALL show the action's label and its currently-resolved key. The one exception preserving its pre-existing boundary is the classic fullscreen agent view, where the global action set stays gated off exactly as it already is for ordinary keypresses — except the jump switcher (`global.switcher`), which the agent view's palette SHALL still list because the switcher was an agent-view action before it became global and its key still fires there.

#### Scenario: Row list reflects the invoking context

- **WHEN** the palette is opened from the classic agent view
- **THEN** its rows are drawn from the agent view's own applicable action set plus the jump switcher row, and no other global action appears

#### Scenario: Hera pane focus unions the focused pane's own actions with the Hera tab's rail — never another tab's rail

- **WHEN** the palette is opened while a Hera coordinator or worker terminal pane holds focus
- **THEN** its rows include that pane's own focused-element actions plus the Hera rail's action set (acting on the rail's current selection), and do NOT include the plain task list's or Settings' action sets

#### Scenario: Task-list and Settings palettes stay scoped to their own tab's rail

- **WHEN** the palette is opened from the plain task list, or from the Settings tab
- **THEN** its rows include that tab's own action set plus the global action set, and do NOT include the Hera rail's action set

#### Scenario: Each row shows the action's bound key

- **WHEN** the palette is open
- **THEN** every row displays both the action's human-readable label and the key chord currently bound to it
