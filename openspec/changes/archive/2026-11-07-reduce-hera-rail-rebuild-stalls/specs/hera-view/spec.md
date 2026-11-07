## MODIFIED Requirements

### Requirement: Debounced rail refresh on the UI thread (area 6)

The system SHALL drive rail rebuilds from the app tick and tab entry through a goroutine-free, timer-free debounced `Refresher`. `Schedule()` coalesces bursts into one rebuild opportunity per debounce window; tab entry forces an immediate flush. A FORCED refresh (tab entry and every interactive hera mutation's immediate-refresh path) SHALL run synchronously on the tview thread so the user's action is reflected at once. A TICK-driven rebuild opportunity SHALL NOT run `BuildModel`, tier annotation or the PR-meta read on the tview thread: it SHALL snapshot its inputs on the tview thread (value copies of the task list, cloned copies of the four runtime maps, the store's data-version fingerprint, and a generation number), run the build on a single background goroutine from that snapshot only, and publish the result back onto the tview thread, where `SetModel`, the PR-meta feed and the selection re-derivation/pane rebind run exactly as for a synchronous rebuild. No `*model.Task` and no runtime map SHALL be shared between the tview thread and the background build.

Within a tick-driven opportunity, the system SHALL skip starting a build when a cheap change-detection check proves nothing that could affect the rendered rail has changed since the last completed rebuild: a SQLite `PRAGMA data_version` fingerprint of the underlying store (unchanged) AND all four per-tick runtime maps (`needsInput`, `sessionIdle`, `sessionRunning`, `sustainedActive`) equal to their values at the last rebuild. The very first opportunity SHALL always run. A store that does not expose a data-version fingerprint (remote mode's nil reader; a test double) SHALL always be treated as changed. Because a write made through the SAME store connection that performs the tick's reads does not change that connection's `PRAGMA data_version` (a documented SQLite blind spot), every interactive hera mutation's immediate-refresh path SHALL also invalidate the cached fingerprint. The fingerprint and maps recorded as "last rebuild" SHALL be those captured in the snapshot BEFORE the build, so a write that lands during a build still triggers the next rebuild.

At most one background build SHALL run at a time. An opportunity arriving while one runs SHALL cause exactly one follow-up build when it finishes. Every forced refresh, gate invalidation and model replacement SHALL advance the generation; a publish whose generation is older than the current one SHALL be dropped without touching the rail. A built model deeply equal to the last published model SHALL be dropped without calling `SetModel` or rebinding panes (the gate still advances), so writes that do not change what the rail renders cost the tview thread nothing. When no UI-queue function is wired (unit tests, a bare `New()`), the tick path SHALL fall back to the synchronous inline rebuild. The async path SHALL log build duration, publish, stale drop and unchanged drop via uxlog.

When the App has already fetched the full task list this tick, the rebuild SHALL reuse that snapshot rather than performing a second full task-list fetch of its own; an opportunity for which no snapshot has been supplied SHALL fetch it itself.

Derived from: `internal/tui/hera/refresher.go` (`Refresher`), `internal/tui/hera/page.go` (`ScheduleRefresh`, `doRefresh`, `shouldRebuild`/`markRebuilt`, async build/publish), `internal/tui/hera/panes.go` (`applySelection` re-run), `internal/db` (`DB.DataVersion`), `internal/tui/heraactions.go` (`heraRefresh` fingerprint invalidation), `context/knowledge/gotchas/hera-view.md`.

#### Scenario: Burst of writes coalesces to one rebuild

- **WHEN** several store writes schedule refreshes within one debounce window
- **THEN** the rail rebuilds once

#### Scenario: Tab entry forces a fresh rail

- **WHEN** the Hera tab is opened
- **THEN** the refresher flushes immediately and synchronously so the rail is current the instant the tab appears

#### Scenario: A quiescent tick skips the rebuild entirely

- **WHEN** a rebuild opportunity arrives and neither the store's data-version fingerprint nor any of the four runtime maps has changed since the last rebuild
- **THEN** no build is started and the rail keeps its last-built model unchanged

#### Scenario: A DB-only change (no runtime-map change) still triggers a rebuild

- **WHEN** the store's data-version fingerprint has changed since the last rebuild but none of the four runtime maps differ
- **THEN** a build is started

#### Scenario: A runtime-only change (no DB write) still triggers a rebuild

- **WHEN** the data-version fingerprint is unchanged but at least one of `needsInput`/`sessionIdle`/`sessionRunning`/`sustainedActive` differs from the last-rebuild snapshot
- **THEN** a build is started, so the spinner/needs-input glyphs never freeze while the underlying DB rows are stable

#### Scenario: A local hera mutation is never missed by the gate

- **WHEN** a TUI-side hera mutation (pin, status step, kanban step, hide, spawn, nuke) writes through the same store connection the tick reads from, and its handler calls the existing immediate-refresh path
- **THEN** the cached fingerprint is invalidated as part of that path, so the next opportunity builds regardless of what the fingerprint alone would report

#### Scenario: A store without a data-version fingerprint always rebuilds

- **WHEN** the reader does not implement the fingerprint (remote mode, or a test double)
- **THEN** every opportunity runs a full build and publish

#### Scenario: The Hera model rebuild reuses a supplied task snapshot instead of re-fetching

- **WHEN** the App has already fetched the task list this tick and supplies it to the Hera rebuild
- **THEN** the rebuild uses that snapshot and performs no second underlying task-list fetch of its own

#### Scenario: A tick-driven build runs off the tview thread

- **WHEN** a tick opportunity passes the change gate and a UI-queue function is wired
- **THEN** `BuildModel` executes on a goroutine other than the tview thread, and `SetModel` executes on the tview thread after the build completes

#### Scenario: The background build shares no mutable state with the tview thread

- **WHEN** the tview thread mutates its task list and runtime maps while a background build is running
- **THEN** the build still reads only its own copies, and the race detector reports no race

#### Scenario: A stale publish is dropped

- **WHEN** a forced refresh, gate invalidation or newer model replacement advances the generation while a background build is in flight
- **THEN** that build's result is dropped without calling `SetModel` and a stale-drop line is logged

#### Scenario: A write during a build is not lost

- **WHEN** a store write changes the data-version fingerprint after the snapshot was taken but before the build publishes
- **THEN** the recorded fingerprint is the snapshot's, so the next opportunity starts another build

#### Scenario: An unchanged model is dropped without UI work

- **WHEN** a build completes whose model is deeply equal to the last published model (for example after a PR-poller rewrite of identical `pr/state` values)
- **THEN** `SetModel` and pane rebinding are not called, the cursor and panes are untouched, and an unchanged-drop line is logged

#### Scenario: One build at a time with a single follow-up

- **WHEN** several opportunities arrive while a build is running
- **THEN** exactly one further build runs after it finishes

#### Scenario: No UI-queue function falls back to the inline path

- **WHEN** the page has no UI-queue function wired
- **THEN** a tick opportunity rebuilds synchronously, identical to behavior before this change

#### Scenario: Selection, fold state and re-arm semantics survive an async publish

- **WHEN** a published model replaces the rail's model
- **THEN** cursor identity, fold state, sticky reveal and the needs-input excursion re-arm behave exactly as for a synchronous `SetModel`
