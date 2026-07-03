# Customizable keybindings via config.toml

## Why

Argus advertises (in the `config.toml` docstring and README) that keybindings
are customizable "the same role alacritty.toml plays for Alacritty," but the
support was never built: the `config.Keybindings` struct is parsed and stored yet
**never read** by any dispatch code — all ~50 bindings are hardcoded `case`
branches. README §`[keybindings]` even carries a ⚠️ warning that setting them
"has no effect yet." This change ships that planned work: a single `keymap`
package that drives every dispatch site and the help overlay, customizable via
context-scoped `config.toml` tables, with live reload — within the documented
limits where rebinding would conflict with the agent PTY or break structural
navigation.

## What Changes

- **New `internal/tui/keymap` package** — the single source of truth for
  argus's own bindings: an `Action` inventory (context-scoped), a `Binding`
  value with tcell-accurate matching, a keyspec parser, and a `Keymap` built
  from defaults overlaid by user config + validation.
- **`config.Keybindings` restructured** from a flat 9-field struct (dead code)
  into context-scoped override maps (`[keybindings.<context>]`). Defaults move
  into `keymap`; config carries overrides only. **BREAKING:** the old 9 DB-backed
  keybinding rows are dropped (single-user repo, no migration per policy) and
  swept on startup.
- **Every argus dispatch site refactored** to recognize keys via
  `keymap.Resolve` instead of literals: `handleGlobalKey`, `handleAgentKey`,
  `handleFilePanelKey`, `handleDiffKey`, the task-list / settings / hera-rail
  widgets. Runtime guards and ordering are preserved verbatim; only key
  recognition becomes table-driven.
- **Help overlay generated** from the keymap so it always reflects actual
  bindings (including user overrides).
- **Validation** rejects unparseable specs, intra-context conflicts,
  agent-context bare-rune binds (PTY guard), and overrides of structural keys —
  always falling back to the default and logging, never bricking the TUI.

## Limitations (out of scope, deliberately)

- Plugin-view keys stay fully surrendered (only the double-Ctrl+Q failsafe).
- Structural keys remain literal: Esc/Enter/Tab/Backtab in modals & forms,
  agent-view Esc-refocus / Enter-restart, settings Left→rail, the hera
  focus-ladder, Ctrl+C / Ctrl+Q failsafe, and arrow-key navigation.
- Agent-context bindings must carry a modifier.

## Impact

- Affected specs: **keybindings** (new), `config-management` (config.toml now
  carries keybinding overrides), `tui-shell` (dispatch now keymap-driven).
- Affected code: new `internal/tui/keymap`; `internal/config/config.go`,
  `internal/db/{config,migrate}.go`, `internal/tui/{app,settings}.go`,
  `internal/tui/taskview/tasklist.go`, `internal/tui/hera/{page,rail}.go`,
  `internal/tui/modal/help.go`.
- Docs: README §Keybindings + §`[keybindings]`, gotchas/keybindings.md.
