## 1. Reproduce and pin down the failure

- [x] 1.1 Add a mobile browser regression test that simulates stale `visualViewport` height/offset at task opening and verifies the detail covers the viewport after reconciliation.
- [x] 1.2 Cover keyboard dismissal after Send, including a missing `scrollend` for the programmatic terminal scroll, and foreground return; retain the existing terminal momentum-scroll assertion.

## 2. Fix viewport reconciliation

- [x] 2.1 Reconcile detail geometry on open, after Send's programmatic scroll and keyboard dismissal, and on foreground return; keep viewport writes deferred during genuine terminal touch/scroll.
- [x] 2.2 Reconcile previously applied keyboard height/offset against the current visual viewport; refit xterm after geometry settles.

## 3. Verify and document

- [x] 3.1 Run focused browser and Go tests, `make test`, `make test-cover`, and the required `make pre-pr` gate before any PR push.
- [x] 3.2 Bump `SW_VERSION` and add the iOS viewport gotcha to `context/knowledge/gotchas/web-remote.md`.
- [x] 3.3 Check frontend parity: no shared REST or daemon contract changes are expected.
