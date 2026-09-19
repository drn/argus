## Context

Top-level tabs are selected by `1`/`2`/`3`; plain Left/Right deliberately fall through to each active view. Hera separately intercepts modified arrows using the existing loose `ModCtrl|ModAlt` check because terminals may report the Cmd chord as either modifier. Within Hera, Cmd+Left/Right walks the rail → coordinator → agent focus ladder, but retreating from the rail is currently a no-op.

The requested behavior crosses an application boundary, while the setting UI and persistence live in the Settings view and DB-backed configuration.

## Goals / non-goals

**Goals:**

- Provide an opt-in spatial transition between the Tasks list and Hera rail.
- Preserve existing navigation when disabled, in text-filter modes, and within Hera content panes.
- Use the same modified-arrow detection convention as existing Hera navigation.
- Persist and expose the preference through the Appearance settings category.

**Non-goals:**

- Restoring generic Left/Right tab cycling.
- Extending the shortcut to Settings, agent fullscreen mode, Hera content panes, the web app, or the macOS app.
- Making this structural shortcut rebindable.

## Decisions

### Route the boundary chord at the application level

The app input capture already knows the active tab, task-filter state, and Hera focus state, and owns `switchTab`. It will intercept only the two qualifying boundary chords before forwarding the event to the focused view. This avoids coupling `HeraPage` to top-level tabs or callbacks and keeps its existing internal ladder unchanged.

Alternative considered: add a Hera callback fired when `FocusMachine.Retreat` reaches the rail. That spreads one cross-tab preference across the Hera package and still does not solve the Tasks-side chord.

### Persist a UI boolean that defaults off

Add a DB-backed `ui.cross_tab_arrows` boolean to `UIConfig`, defaulting to false, and render it as an Appearance boolean row. Default-off preserves current behavior for existing installations and users.

Alternative considered: make the navigation unconditional. That would silently reclaim a modified key from the task list and change the rail-edge behavior without user consent.

### Suppress the transition during filtering

The boundary shortcut applies only when the Tasks list or Hera rail is in normal navigation mode. Filtering keeps the modified arrow available to the active input and prevents an accidental tab switch while editing a query.

### Treat the shortcut as TUI-only

This preference governs terminal key events and does not alter the daemon's REST-exposed surface. Web and macOS parity therefore requires evaluation but no implementation.

## Risks / trade-offs

- **Terminal modifier reporting varies** → Reuse the existing loose `ModCtrl|ModAlt` predicate already covered by Hera navigation tests.
- **A global interception could steal pane input** → Gate Hera-to-Tasks strictly on `FocusRail`; content panes retain the existing focus ladder.
- **Settings can drift from live behavior** → Read the effective config at key-dispatch time through the store, matching other live UI preferences.
- **Help could imply the shortcut is always active** → Label it as conditional on the Appearance option.

## Migration plan

No schema migration is required because configuration is stored as key/value rows. Existing installations have no `ui.cross_tab_arrows` key and therefore receive the false default. Rollback ignores the extra row.

## Open questions

None.
