## 1. Pane-level state

- [x] 1.1 Add `closedOutDismissedOnce` field to `TerminalPane`, set by `DismissClosedOutBanner`
- [x] 1.2 Add `ClosedOutReadyToRevive()` accessor (`!closedOutBannerShown && closedOutDismissedOnce`)
- [x] 1.3 Add `ClearClosedOutState()` to reset both closed-out flags
- [x] 1.4 Reset `closedOutDismissedOnce` in `ResetVT`, alongside `closedOutBannerShown`

## 2. DB primitive

- [x] 2.1 Add `db.ClearHeraCloseout(taskID)`: clears `meta:hera.ready_to_close` via `ClearHeraReadyToClose`, then resets any live binding's terminal (`done`/`failed`) role status to `working`
- [x] 2.2 Unit tests: both signals individually and together, non-terminal status left alone, no binding, never-closed-out no-op

## 3. App-level revive

- [x] 3.1 Extend `App.reattachClosedOut` with a third branch: when `pane.ClosedOutReadyToRevive()`, call `forceReviveClosedOut` instead of re-arming the banner
- [x] 3.2 Add `App.forceReviveClosedOut(pane, t)`: calls `db.ClearHeraCloseout` (local-mode only), `pane.ClearClosedOutState()`, then `startSession(t)`
- [x] 3.3 Update `heraReattachClosedOut`'s and `reattachClosedOut`'s doc comments to describe the three-step sequence

## 4. Tests

- [x] 4.1 Update `TestSmoke_HeraReattachClosedOutTogglesBannerThenReadOnly`'s third-Enter assertion (Hera tab)
- [x] 4.2 Add `TestHandleAgentKey_ThirdEnterRevivesClosedOutTask` (plain Tasks tab)
- [x] 4.3 Add `TestTerminalPane_ClosedOutReadyToRevive_TracksThirdStep`

## 5. Documentation

- [x] 5.1 Add gotcha bullet to `context/knowledge/gotchas/hera-view.md`
- [x] 5.2 Bump `context/knowledge/index.md`'s bullet count for `hera-view.md`
- [x] 5.3 Write proposal/design/delta-spec, archive into base spec before merge
