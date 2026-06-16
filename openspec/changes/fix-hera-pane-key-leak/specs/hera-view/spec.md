# Hera View

## ADDED Requirements

### Requirement: Global key shortcuts are focus-gated on the Hera tab (area 5)

While the Hera tab is active AND focus is inside a content pane (the coordinator pane or the agent/details region — i.e. NOT the rail), the global key handler SHALL NOT consume the shortcuts it otherwise intercepts in the task-list mode: `q` (quit), `1`/`2`/`3` (tab switch), `?` (help), `Ctrl+C` (quit), and `Ctrl+L` (screen Sync). These keys SHALL instead fall through to `HeraPage`, which forwards them to the focused pane's PTY, because a focused pane is a live terminal. While the RAIL holds focus these globals SHALL continue to apply (the rail is not a content pane), so the operator escapes a pane with `Ctrl+Q` to use them again. This mirrors how the agent view (`modeAgent`) surrenders the same keys to its PTY.

Derived from: `internal/tui/app.go` (`App.heraPaneFocused`), `internal/tui/app.go` (`handleGlobalKey` rune-switch guard + `Ctrl+C`/`Ctrl+L` guards).

#### Scenario: q is typed into a focused pane, not a quit

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `q`
- **THEN** argus does not quit — the key falls through to the focused pane's PTY

#### Scenario: Ctrl+C interrupts the focused agent instead of quitting

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `Ctrl+C`
- **THEN** argus does not quit — `Ctrl+C` is delivered to the focused pane's PTY (interrupt the agent)

#### Scenario: tab-switch and help digits reach the pane

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `1`, `2`, `3`, or `?`
- **THEN** the tab does not switch and help does not open — each key falls through to the focused pane's PTY

#### Scenario: rail focus keeps the globals

- **WHEN** the Hera tab is active and the RAIL holds focus
- **THEN** `q` quits, `1`/`2`/`3` switch tabs, and `?` opens help (the rail is not a content pane, so the globals are not gated)
