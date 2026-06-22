# Tasks

## 1. Re-resolve a present-but-dead pane session

- [x] 1.1 `reconcileOne`: when the bound pane holds a session that is `!Alive()`, re-resolve via the provider and swap in a fresh, live, DISTINCT handle (`ForceResyncPTY` after). Leave the pane untouched when the provider yields nil (process gone) or the same dead handle (cache not yet evicted).
- [x] 1.2 Keep the late-bind path (nil session → bind whatever non-nil resolves) and the live-session-present early return unchanged.
- [x] 1.3 Add `paneBinding(tp)` → (boundID, label) so `forwardKey` can re-resolve without threading extra state.

## 2. Stop dropping keystrokes silently

- [x] 2.1 `forwardKey`: when the session is nil/`!Alive()`, uxlog the drop (`[hera]`/`[hera-view]` prefix), call `reconcileOne` to re-resolve, then retry the write on the fresh handle; log again if still no live session.

## 3. Tests

- [x] 3.1 `panes_test.go`: a present-but-dead session is replaced by a fresh live handle on `Reconcile()`.
- [x] 3.2 `panes_test.go`: a dead session with no live replacement (and with the same not-yet-evicted handle) is retained, not thrashed.
- [x] 3.3 `panes_test.go`: `forwardKey` into a dead pane re-resolves and the keystroke reaches the fresh handle; with no live replacement it is dropped (not delivered to the dead handle), no panic.

## 4. Docs + validate

- [x] 4.1 `context/knowledge/gotchas/hera-view.md`: document the present-but-dead re-resolution invariant.
- [x] 4.2 `make pre-pr` passes clean.
