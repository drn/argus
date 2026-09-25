## Why

The attached iPhone screenshot shows a task detail panel beginning about halfway down the screen, with the task-list chrome exposed above it and the bottom of the panel clipped. The user reports that this happens when sending composed text. The panel's height and vertical translation are controlled by `syncVisualViewport()`. Send also calls `term.scrollToBottom()`, which can set the terminal's `isTermScrolling` guard just as tapping Send dismisses the soft keyboard. The viewport resize is then deferred until terminal `scrollend`; if the browser omits that event for the programmatic scroll, the keyboard-sized height and offset remain applied. `openDetail()` also reveals the panel without recalculating viewport geometry, so stale values can persist on navigation.

## What Changes

- Reconcile the detail panel's viewport geometry after Send's programmatic terminal scroll, when it opens, and when the app returns to the foreground, as well as during viewport resize/scroll and keyboard transitions.
- Prevent a stale keyboard offset or height from leaving the task-list page exposed when the keyboard is closed, while preserving the existing above-keyboard layout and terminal-scroll momentum guard.
- Add a mobile browser regression test for opening a task after a scrolled task list or keyboard transition, and for recovery from stale viewport geometry.
- Bump the service-worker shell version when the SPA changes, and document the non-obvious iOS viewport invariant.

## Capabilities

### Modified Capabilities

- `mobile-pwa`: the task detail remains aligned with the visible viewport through task opening, soft-keyboard transitions, and foreground return.

## Impact

- Web/PWA only (`internal/api/static/index.html`, `sw.js`, `web-tests/`). No REST contract or daemon state change; TUI and macOS clients do not use this viewport code.
- OpenSpec remains local documentation; `make pre-pr` remains the quality gate.
