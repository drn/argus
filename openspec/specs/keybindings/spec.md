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

### Requirement: Task/role switcher binds to ctrl+j, freeing ctrl+k

The system SHALL bind the task/role switcher action (`agent.switcher`) to `ctrl+j` by default in the agent context. `ctrl+k` — its previous default — SHALL NOT resolve to the switcher any longer, freeing it for the global command palette action. The switcher SHALL also be reachable from the plain Tasks tab (no live agent view, no Hera focus): `handleGlobalKey` SHALL resolve the agent context's own `agent.switcher` binding directly when the Tasks tab is active, so a config override of `agent.switcher` applies uniformly across the classic agent view and the plain Tasks tab.

#### Scenario: ctrl+j opens the switcher in the classic agent view

- **WHEN** the user presses `ctrl+j` while a classic agent view is focused
- **THEN** the task/role switcher opens

#### Scenario: ctrl+k no longer opens the switcher

- **WHEN** the user presses `ctrl+k` while a classic agent view is focused
- **THEN** the switcher does not open (the key now resolves to the command palette action instead)

#### Scenario: ctrl+j opens the switcher from the plain Tasks tab

- **WHEN** the user presses `ctrl+j` while the plain Tasks tab holds focus (no agent view, no Hera focus)
- **THEN** the task/role switcher opens, and closing it (Esc) returns focus to the task list

### Requirement: A rebindable global action may bypass per-mode gating for guaranteed global reach

The system SHALL support marking a `CtxGlobal` action (e.g. `global.palette`) so it is resolved and dispatched ahead of the normal mode-gated action switch, guaranteeing it fires regardless of `mode` or focus region — mirroring the reach of the existing non-rebindable Ctrl+Q/Ctrl+Z structural interceptions — while remaining a fully rebindable action: it still resolves through the standard `Resolve(context, event)` path, accepts config overrides, and appears in the generated help overlay like any other action.

#### Scenario: The action fires from every mode

- **WHEN** an action is marked for unconditional dispatch and its bound key is pressed in any mode (task list, classic agent view, or a Hera-focused region)
- **THEN** the action fires in every one of those modes, unlike ordinarily mode-gated `CtxGlobal` actions

#### Scenario: The action remains rebindable and documented

- **WHEN** a config override rebinds an unconditionally-dispatched action to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for any other rebindable action

### Requirement: Global jump-to-next-needs-input binds to ctrl+g

The system SHALL provide a global `global.jump_needs_input` action, default `ctrl+g`, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism `global.palette` (`ctrl+k`) already established — resolved once in `handleGlobalKey` and switched on before the `a.mode`/`suppressRune` gate, so it fires regardless of mode or focus region (the classic fullscreen agent view, the plain Tasks tab, or any Hera focus region) without requiring a separate per-surface literal case the way the task/role switcher (`ctrl+j`) does.

#### Scenario: ctrl+g fires from every mode

- **WHEN** the user presses `ctrl+g` in the classic agent view, the plain Tasks tab, the Hera rail, or a focused Hera pane
- **THEN** the jump-to-next-needs-input action fires in every one of those contexts, and the key byte never reaches a focused pane's live PTY

#### Scenario: ctrl+g remains rebindable and documented

- **WHEN** a config override rebinds `global.jump_needs_input` to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for `global.palette`

### Requirement: Global rail-restore binds to ctrl+b

The system SHALL provide a global `global.restore_rail` action, default `ctrl+b`, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism `global.palette` (`ctrl+k`) and `global.jump_needs_input` (`ctrl+g`) already establish — resolved once in `handleGlobalKey` and switched on before the `a.mode`/`suppressRune` gate, so it fires regardless of mode or focus region (the classic fullscreen agent view, the plain Tasks tab, or any Hera focus region) without requiring a separate per-surface literal case.

`ctrl+b` was chosen over the originally-discussed `ctrl+shift+g` because the keyspec grammar (see "Keyspec grammar" above) restricts the `shift` modifier to arrow/navigation keys — `ctrl+<letter>` is a single C0 control byte with no bit available to carry Shift at the terminal-encoding level, so no terminal can distinguish `ctrl+g` from a "shifted" variant regardless of keymap support. `ctrl+b` is unused across every context participating in the global unconditional-dispatch bucket (`CtxGlobal`, `CtxAgent`, `CtxHeraRail`, `CtxTaskList`) and does not alias a structural key the way `ctrl+i` (Tab) or `ctrl+m` (Enter) would.

#### Scenario: ctrl+b fires from every mode

- **WHEN** the user presses `ctrl+b` in the classic agent view, the plain Tasks tab, the Hera rail, or a focused Hera pane
- **THEN** the rail-restore action fires in every one of those contexts, and the key byte never reaches a focused pane's live PTY or the agent session

#### Scenario: ctrl+b remains rebindable and documented

- **WHEN** a config override rebinds `global.restore_rail` to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for `global.palette`/`global.jump_needs_input`

