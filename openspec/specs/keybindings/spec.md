# keybindings Specification

## Purpose
TBD - created by archiving change customizable-keybindings. Update Purpose after archive.
## Requirements
### Requirement: Context-scoped keymap is the single source of truth

The system SHALL resolve every rebindable argus keystroke through a `keymap` package whose default bindings reproduce the historical hardcoded behavior, scoped by `Context` (global, tasklist, agent, filepanel, diff, settings, hera_rail). Each dispatch site SHALL recognize keys by `Resolve(context, event)` rather than literal key comparison.

#### Scenario: Default bindings unchanged

- **WHEN** no `[keybindings]` overrides are present in config.toml
- **THEN** every action resolves to the same key it was hardcoded to before this change (e.g. `n` opens the new-task form, `ctrl+l` opens the agent link picker)

#### Scenario: Per-context disambiguation

- **WHEN** the same physical key is bound to different actions in different contexts (e.g. `s` advances status in the task list but toggles split/unified in the diff view)
- **THEN** `Resolve` returns the action for the active context only

### Requirement: Context-scoped config overrides

The system SHALL read user overrides from `[keybindings.<context>]` tables in config.toml, where each entry maps an action id (the suffix after the context) to a keyspec string. Overrides SHALL layer on top of the built-in defaults; absent entries keep their default. Unknown action ids and unknown tables SHALL be ignored without error.

#### Scenario: Rebind an action

- **WHEN** config.toml contains `[keybindings.tasklist]` with `new = "x"`
- **THEN** pressing `x` in the task list opens the new-task form and pressing `n` does nothing

#### Scenario: Live reload

- **WHEN** the user edits a `[keybindings.<context>]` entry while argus is running
- **THEN** the new binding takes effect without a restart (the keymap rebuilds when the config changes)

### Requirement: Keyspec grammar

The system SHALL parse keyspec strings of the form `modifier*('+')base`, where modifiers are `ctrl`/`control`, `cmd`/`opt`/`alt`, and `shift`, and base is either a single printable rune or a named key (`enter`, `return`, `esc`, `escape`, `tab`, `backtab`, `space`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup`, `pgdn`, `backspace`, `delete`). `ctrl+<letter>` SHALL map to the corresponding control key; `cmd`/`opt`/`alt`+arrow SHALL map to the arrow key with the Ctrl+Alt convention; `shift`+arrow SHALL map to the arrow key with Shift.

#### Scenario: Reject unsupported specs

- **WHEN** a keyspec is `ctrl+/`, `ctrl+left`, `ctrl+right`, `shift+<letter>`, or otherwise unparseable
- **THEN** the parser returns an error, the override is ignored with a logged warning, and the default binding is kept

### Requirement: Validation never bricks the TUI

The system SHALL validate overrides at build time and, on any problem, keep the default binding and emit a non-fatal warning. The system SHALL reject: an override that fails to parse; two actions resolving to the same binding within one context (the colliding override is dropped); an agent-context binding without a modifier; and any override targeting a structural keyspec or a non-rebindable action.

#### Scenario: Agent-context bare rune rejected

- **WHEN** config.toml sets an agent-context action to a bare printable rune (e.g. `links = "z"`)
- **THEN** the override is rejected with a warning and the default (`ctrl+l`) is kept, so normal typing still reaches the agent PTY

#### Scenario: Intra-context conflict

- **WHEN** two task-list actions are configured to the same key
- **THEN** the colliding override is dropped with a warning and the defaulted action keeps its binding

### Requirement: Reserved and structural keys are not rebindable

The system SHALL NOT route the following through the keymap, leaving them hardcoded: plugin-view keys (full-surrender contract, only the double-Ctrl+Q failsafe), the Ctrl+C / Ctrl+Q failsafe, modal/form Esc/Enter/Tab/Backtab, agent-view Esc-refocus and Enter-restart, settings Left→rail and Right/Enter-cycle, the hera focus-ladder (Tab/Ctrl+Q/Ctrl+Alt+arrows), and arrow-key navigation.

#### Scenario: Structural key override refused

- **WHEN** config.toml attempts to bind an action to `enter`, `esc`, `tab`, or `backtab`
- **THEN** the override is rejected with a warning and the structural behavior is unchanged

### Requirement: Help overlay reflects active bindings

The system SHALL generate the help overlay's argus sections from the keymap so the displayed keys match the resolved bindings, including user overrides.

#### Scenario: Help shows the override

- **WHEN** the task-list `new` action is rebound to `x`
- **THEN** the help overlay's Task List section shows `x` for "new task"

### Requirement: File-action hotkeys remain available while viewing a diff

The diff-view context (`CtxDiff`) SHALL also resolve the file-action hotkeys
(reveal in Finder, open file, open in editor, open terminal) against the
currently selected file, so a user does not lose these hotkeys the moment
`Enter` displays a file's diff.

#### Scenario: Finder hotkey works while a diff is displayed

- **WHEN** a file's diff is currently displayed (diff mode active)
- **AND** the user presses the reveal-in-Finder key (default `f`)
- **THEN** the currently selected file is revealed in Finder, same as when the
  file panel (not the diff) has focus

#### Scenario: Open/editor/terminal hotkeys work while a diff is displayed

- **WHEN** a file's diff is currently displayed (diff mode active)
- **AND** the user presses the open-file, open-in-editor, or open-terminal key
  (defaults `o`/`e`/`t`)
- **THEN** the corresponding action runs against the currently selected file
  (or the worktree directory, for open-terminal), same as when the file panel
  has focus

