## Why

Cmd+Left/Right already walks Hera's pane ladder and, behind an opt-in flag, hops Tasks ⇄ Hera rail. The operator wants to traverse all of argus with Cmd+arrows alone, and did not know the flag existed. Extending the hops into one continuous spatial chain, on by default, removes the need for tab-number shortcuts during ordinary navigation.

## What changes

- One continuous chain: Tasks ⇄ Hera rail ⇄ Hera coord pane ⇄ Hera agent pane ⇄ Settings left pane ⇄ Settings right pane.
- New hop: Cmd+Right from Hera's rightmost present pane (agent, else coord) switches to Settings and focuses its LEFT pane.
- New hop: Cmd+Left from Settings' left pane switches to Hera and focuses its rightmost present pane (enter from the right → land on the right).
- Within Settings, Cmd+Right moves left → right pane and Cmd+Left right → left pane; Cmd+Right on the right pane is a consumed no-op (end of chain).
- `ui.cross_tab_arrows` default flips to ON (absent row means enabled); the Settings toggle remains so it can be turned off. Detail text describes the full chain.
- Existing guards are kept: never from agent view or modals, never while a filter or inline edit has focus, and Hera fullscreen behavior is unchanged.
- Help overlay and README keybinding/config reference updated.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `tui-shell`: extend the optional boundary navigation into the full chain.
- `settings-view`: default the preference to enabled and describe the full chain.

## Non-goals

- **Web SPA and macOS app:** pane/tab keyboard traversal has no REST surface (parity is defined at the REST-exposed surface), so neither client gets an equivalent. Named intentional gap, not an oversight.
- No second setting; all hops ride `ui.cross_tab_arrows`.
- No migration of stored values: an explicit stored `false` is honored.

## Impact

`internal/tui` key routing, `FocusMachine` helpers, Settings pane-focus accessors, config default, help/README/gotchas, and tests. No REST, daemon, or protocol changes.
