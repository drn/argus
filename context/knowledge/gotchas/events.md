# Events ring + SSE stream — gotchas

## Emission MUST happen outside `db.mu`

`db.Add`, `db.Update`, `db.Rename`, `db.SetArchived`, `db.InsertMessage`, and `db.AckMessages` each emit via `events.Emit` AFTER releasing `d.mu`. The installed sink (`api.Server.Emit`) re-acquires `d.mu` inside `db.InsertEvent`, so emitting from a still-locked region deadlocks the entire DB. Every wired method uses a `*Locked` helper + post-unlock `events.Emit(...)` to keep the two phases separate. If you add a new emission site inside the `db` package, follow the same split — do NOT call `events.Emit` between `d.mu.Lock()` and the matching unlock.

## SSE handler subscribes BEFORE snapshotting `LatestEventID`

`internal/api/events_stream.go:handleEventsStream` subscribes to the bus first, then captures `replayEnd := db.LatestEventID()`, then replays `EventsSince(since, 0)`, then drains live events skipping any `ev.ID <= replayEnd`. Reversing the order (snapshot-then-subscribe) opens a window where an event committed mid-snapshot would be delivered twice (replay tail + live) or land out of order. The fence is the only thing keeping the stream lossless and dupe-free; don't reorder it.

## `events.SetSink` is global; tests must save/restore

The package-level sink is one atomic pointer. Tests that install a recording sink MUST register a `t.Cleanup` that restores the prior sink — otherwise a subsequent test inherits the recorder and sees events from unrelated work. `events.SetSink` returns the previous sink for this round-trip pattern.

## Ring eviction is based on `id` rank, not row count delete

`db.InsertEvent` evicts via `DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT eventsCapForTest)`. Using `NOT IN` rather than a row-count math keeps the AUTOINCREMENT counter intact — cursors stay monotonic across evictions, so a plugin reconnecting with `since=<old>` triggers the `resync` path correctly. A naive `DELETE FROM events ORDER BY id ASC LIMIT <overflow>` would work too but is harder to reason about under concurrent inserts; the current form is purely declarative against the desired post-state.

## Idle watcher runs unconditionally

`api.Server.New` starts `idleWatcher` even when `push == nil`. The watcher does two things: emits `session.idle` events (plugin-visible, always) and triggers Web Push (gated on `s.push != nil` inline). Before PR 2 this goroutine was push-gated; now the gate is per-call so plugins running on a daemon without push still see idle transitions. The `idleTransitioned` helper is a stateless predicate that reads `state.idleNow[id]` BEFORE the mutation inside `shouldFireIdlePush` — the two helpers must run in order (transition check, then push gate) because they share state.

## `task.completed` is emitted in addition to `task.status_changed`

When `db.Update` detects a status change to `StatusComplete`, it emits BOTH events (in that order). Plugins filtering on `task.completed` get a precise hook; plugins watching `task.status_changed` see the full transition stream. This double-emit is intentional — there is no "promote status_changed to completed" downstream; the contract is "both fire."

## `session.needs_input` is daemon-authoritative, idle-gated, and sticky

`api.Server.detectNeedsInputTick` (called each `idleWatcherTick`) scans idle sessions with the shared `agent.DetectNeedsInput` heuristic and publishes the set onto the runner (`SetNeedsInputIDs`) so `/api/tasks`' `needs_input` field is correct with NO TUI attached. Two invariants are load-bearing and non-obvious:

- **Idle-gate + sticky carry-forward, or the SSE events oscillate.** Detection only runs on idle tasks (a still-streaming agent that flashes `❯ 1.` transiently is not blocked). But Claude's prompt UI emits periodic animation bytes that briefly knock a blocked session OUT of the idle set — without the sticky pass in `computeNeedsInput` (re-check previously-flagged tasks that are still running), the flag and its `session.needs_input` events would flap every few ticks. Mirrors the TUI's `detectNeedsInputSticky`; do not drop either half.
- **Reads the session ring (`RecentOutputTail`), not the on-disk log.** The daemon is the sole PTY reader so the ring is always populated — correct without a TUI. The TUI's `detectNeedsInput` reads the disk log instead, because in daemon-client mode its local ring only fills after the user opens a stream. Same heuristic, different byte source by process.

One event type carries both edges: the payload `{"needs_input":true|false}` distinguishes enter from clear (there is no separate "cleared" type). `state.needsInputNow` is replaced wholesale each tick, so an exited session (absent from running/idle) drops out of the set and fires a clear event via the diff automatically.
