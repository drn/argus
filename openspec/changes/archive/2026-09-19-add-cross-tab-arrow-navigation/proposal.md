## Why

Tasks and Hera are adjacent working views, but moving between them currently requires a tab-number shortcut even though Cmd+Left/Right already provides spatial navigation inside Hera. An opt-in boundary transition makes the two views feel like one horizontal workspace without changing the established behavior for users who prefer explicit tab selection.

## What changes

- Add an Appearance setting that enables Cmd+Left/Right navigation across the Tasks–Hera boundary and defaults to disabled.
- When enabled, Cmd+Right from the unfiltered Tasks list opens Hera with the rail focused.
- When enabled, Cmd+Left from Hera's unfiltered, focused rail returns to the Tasks list.
- Preserve Hera's existing Cmd+Left/Right focus-ladder behavior in coordinator and agent panes, and preserve all existing behavior while the option is disabled or either rail is filtering.
- Document the optional shortcut in the TUI help and README keybinding reference.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `tui-shell`: Replace the stale plain-arrow tab-navigation contract with the current numeric-tab behavior plus the new optional Cmd+Left/Right Tasks–Hera boundary navigation.
- `settings-view`: Add and persist the Appearance toggle that controls cross-tab arrow navigation.

## Impact

The change affects UI configuration loading and persistence, the Settings Appearance rows, top-level key routing, TUI help/reference text, and tests for config, settings, key dispatch, and the real event loop. It changes no REST contract and requires no web or macOS client work.
