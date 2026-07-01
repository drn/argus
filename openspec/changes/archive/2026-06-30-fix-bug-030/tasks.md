## 1. Auto-expire transient notices

- [x] 1.1 Add `StatusNoticeTTL` (15s) and an injectable `now func() time.Time`
  clock to `StatusBar` (defaults to `time.Now` in `NewStatusBar`); `SetError` /
  `SetInfo` stamp `errExpiresAt` / `infoExpiresAt` from the clock so each call
  gets a full TTL and resets any prior window (BUG-030).
- [x] 1.2 `expireNotices` drops a notice once `now()` reaches its `expiresAt`;
  call it on every read path (`Draw`, `Error`, `Info`) so an expired notice
  paints as the default counts and the accessors stay truthful. `ClearError` /
  `ClearInfo` also zero the matching `expiresAt`.
- [x] 1.3 The revert rides the app's existing unconditional ~1s `onTick`
  `QueueUpdateDraw` — no new goroutine/timer, and no `screen.Sync()`
  (`gotchas/ui-threading.md`).

## 2. Tests

- [x] 2.1 An error notice reverts to the default counts once `StatusNoticeTTL`
  elapses (injected clock, no real sleep), and `Error()` then returns "".
- [x] 2.2 Same for an info notice via `SetInfo` / `Info()`.
- [x] 2.3 A notice stays visible just before its TTL and only clears after it.
- [x] 2.4 A second notice set mid-window honours its own full-TTL reset window
  (not stacked, not cleared early).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/ui-threading.md`
  (status-bar notices auto-expire after ~15s; revert via `QueueUpdateDraw`, never
  `Sync`) and bump the index bullet count.
