## MODIFIED Requirements

### Requirement: Task/role switcher binds to ctrl+j, freeing ctrl+k

The system SHALL bind the jump switcher action (`global.switcher`, in the global context) to `ctrl+j` by default, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism as `global.palette` and `global.jump_needs_input`, so one dispatch point covers the classic agent view, the plain Tasks tab, and every Projects focus region. The former agent-context `agent.switcher` action SHALL no longer exist. `ctrl+k` SHALL NOT resolve to the switcher, leaving it to the global command palette action.

#### Scenario: ctrl+j opens the switcher in the classic agent view

- **WHEN** the user presses `ctrl+j` while a classic agent view is focused
- **THEN** the jump switcher opens

#### Scenario: ctrl+k no longer opens the switcher

- **WHEN** the user presses `ctrl+k` while a classic agent view is focused
- **THEN** the switcher does not open (the key resolves to the command palette action instead)

#### Scenario: ctrl+j opens the switcher from the plain Tasks tab

- **WHEN** the user presses `ctrl+j` while the plain Tasks tab holds focus (no agent view, no Projects focus)
- **THEN** the jump switcher opens, and closing it (Esc with no filter) returns focus to the task list

#### Scenario: ctrl+j remains rebindable and documented

- **WHEN** a config override rebinds `global.switcher` to a different key
- **THEN** the new key opens the switcher from every context and the help overlay reflects it

### Requirement: Global jump-to-next-needs-input binds to ctrl+g

The system SHALL provide a global `global.jump_needs_input` action, default `ctrl+g`, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism `global.palette` (`ctrl+k`) already established — resolved once in `handleGlobalKey` and switched on before the `a.mode`/`suppressRune` gate, so it fires regardless of mode or focus region (the classic fullscreen agent view, the plain Tasks tab, or any Hera focus region) without a separate per-surface literal case. While the jump switcher popup is open, `ctrl+g` SHALL instead be handled by the popup (cycling its cursor) and SHALL NOT trigger this action.

#### Scenario: ctrl+g fires from every mode

- **WHEN** the user presses `ctrl+g` in the classic agent view, the plain Tasks tab, the Hera rail, or a focused Hera pane
- **THEN** the jump-to-next-needs-input action fires in every one of those contexts, and the key byte never reaches a focused pane's live PTY

#### Scenario: ctrl+g remains rebindable and documented

- **WHEN** a config override rebinds `global.jump_needs_input` to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for `global.palette`

#### Scenario: ctrl+g inside the jump switcher does not jump away

- **WHEN** the jump switcher popup is open and the user presses `ctrl+g`
- **THEN** the popup's cursor moves to its next needs-input row and the global jump action does not fire
