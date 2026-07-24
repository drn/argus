# Keybindings

## ADDED Requirements

### Requirement: Global rail-restore binds to ctrl+b

The system SHALL provide a global `global.restore_rail` action, default `ctrl+b`, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism `global.palette` (`ctrl+k`) and `global.jump_needs_input` (`ctrl+g`) already establish — resolved once in `handleGlobalKey` and switched on before the `a.mode`/`suppressRune` gate, so it fires regardless of mode or focus region (the classic fullscreen agent view, the plain Tasks tab, or any Hera focus region) without requiring a separate per-surface literal case.

`ctrl+b` was chosen over the originally-discussed `ctrl+shift+g` because the keyspec grammar (see "Keyspec grammar" above) restricts the `shift` modifier to arrow/navigation keys — `ctrl+<letter>` is a single C0 control byte with no bit available to carry Shift at the terminal-encoding level, so no terminal can distinguish `ctrl+g` from a "shifted" variant regardless of keymap support. `ctrl+b` is unused across every context participating in the global unconditional-dispatch bucket (`CtxGlobal`, `CtxAgent`, `CtxHeraRail`, `CtxTaskList`) and does not alias a structural key the way `ctrl+i` (Tab) or `ctrl+m` (Enter) would.

#### Scenario: ctrl+b fires from every mode

- **WHEN** the user presses `ctrl+b` in the classic agent view, the plain Tasks tab, the Hera rail, or a focused Hera pane
- **THEN** the rail-restore action fires in every one of those contexts, and the key byte never reaches a focused pane's live PTY or the agent session

#### Scenario: ctrl+b remains rebindable and documented

- **WHEN** a config override rebinds `global.restore_rail` to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for `global.palette`/`global.jump_needs_input`
