## ADDED Requirements

### Requirement: Task/role switcher binds to ctrl+j, freeing ctrl+k

The system SHALL bind the task/role switcher action (`agent.switcher`) to `ctrl+j` by default in the agent context. `ctrl+k` — its previous default — SHALL NOT resolve to the switcher any longer, freeing it for the global command palette action.

#### Scenario: ctrl+j opens the switcher in the classic agent view

- **WHEN** the user presses `ctrl+j` while a classic agent view is focused
- **THEN** the task/role switcher opens

#### Scenario: ctrl+k no longer opens the switcher

- **WHEN** the user presses `ctrl+k` while a classic agent view is focused
- **THEN** the switcher does not open (the key now resolves to the command palette action instead)

### Requirement: A rebindable global action may bypass per-mode gating for guaranteed global reach

The system SHALL support marking a `CtxGlobal` action (e.g. `global.palette`) so it is resolved and dispatched ahead of the normal mode-gated action switch, guaranteeing it fires regardless of `mode` or focus region — mirroring the reach of the existing non-rebindable Ctrl+Q/Ctrl+Z structural interceptions — while remaining a fully rebindable action: it still resolves through the standard `Resolve(context, event)` path, accepts config overrides, and appears in the generated help overlay like any other action.

#### Scenario: The action fires from every mode

- **WHEN** an action is marked for unconditional dispatch and its bound key is pressed in any mode (task list, classic agent view, or a Hera-focused region)
- **THEN** the action fires in every one of those modes, unlike ordinarily mode-gated `CtxGlobal` actions

#### Scenario: The action remains rebindable and documented

- **WHEN** a config override rebinds an unconditionally-dispatched action to a different key
- **THEN** the new key takes effect and the help overlay reflects it, exactly as for any other rebindable action
