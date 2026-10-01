## 1. Config and settings

- [x] 1.1 Flip the `ui.cross_tab_arrows` default to enabled (config + DB load coverage).
- [x] 1.2 Update the Appearance row label and detail text to describe the full chain.

## 2. Navigation

- [x] 2.1 Add `FocusMachine.AtRightmost` / `ToRightmost` and Settings pane-focus accessors, with tests.
- [x] 2.2 Write failing app-level and SimulationScreen tests for every hop in both directions, end-of-chain no-ops, absent agent pane, flag off, filters, Settings inline edit, agent view, Hera fullscreen.
- [x] 2.3 Implement the chain in `handleCrossTabArrow` with `[tui] cross-tab arrow:` logging.

## 3. Docs and verification

- [x] 3.1 Update help overlay (+ assertions), README reference, and the keybindings gotcha.
- [x] 3.2 Run `make pre-pr`.
- [x] 3.3 Archive the change into the base specs before merge.
