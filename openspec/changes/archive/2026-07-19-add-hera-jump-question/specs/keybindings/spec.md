## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Global jump-to-next-needs-input binds to ctrl+g

The system SHALL provide a global `global.jump_needs_input` action, default `ctrl+g`, dispatched via the same unconditional-ahead-of-the-mode-gate mechanism `global.palette` (`ctrl+k`) already established — resolved once in `handleGlobalKey` and switched on before the `a.mode`/`suppressRune` gate, so it fires regardless of mode or focus region (the classic fullscreen agent view, the plain Tasks tab, or any Hera focus region) without requiring a separate per-surface literal case the way the task/role switcher (`ctrl+j`) does.

#### Scenario: ctrl+g fires from every mode

- **WHEN** the user presses `ctrl+g` in the classic agent view, the plain Tasks tab, the Hera rail, or a focused Hera pane
- **THEN** the jump-to-next-needs-input action fires in every one of those contexts, and the key byte never reaches a focused pane's live PTY

#### Scenario: ctrl+g remains rebindable and documented

- **WHEN** a config override rebinds `global.jump_needs_input` to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for `global.palette`
