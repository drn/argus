## MODIFIED Requirements

### Requirement: PTY size alignment on bind (area 6)

The system SHALL resize a bound session to the (narrower) hera pane when binding it, by calling `ForceResyncPTY()` so a session previously sized for the full-width main agent view is resized down on the next Draw, with `SyncPanes()` issuing the Resize RPC off the main thread. Pane operations on the tview thread SHALL use only lock-free/local session methods; the blocking resize RPC runs only from the tick goroutine. The view SHALL NOT use `screen.Sync()` to paper over a size mismatch.

A plain PTY resize (SIGWINCH) only re-flows a session's LIVE UI — it cannot repair scrollback already committed at a different width, because cursor-positioning codes baked into earlier PTY output remain wrong once re-emulated at a new size. When binding a session (coordinator pane or worker/agent pane) whose recorded initial PTY width differs from the current hera pane's width by at least the shared rerender margin, the system SHALL evaluate the SAME kill+resume decision the main agent view applies on entry (`agent.ShouldKickRerender`), using the hera pane's own current width — not the main agent view's. The decision SHALL be skipped when a kick is already pending for the task, when the session lacks a resumable session ID, when the agent is not idle (deferred, not lost), or when the agent is blocked on a user prompt (deferred, never dismissed). The redundant-attach cache SHALL be shared with the main agent view's (keyed by task ID, not by which surface is asking), so a task already evaluated at its current attach width is not re-evaluated on every pane rebind.

The kick decision, once its gates pass, SHALL NOT fire immediately: it SHALL be debounced by a short wall-clock dwell (300ms) so that ordinary rail navigation — which alone swings a bound pane between full-width (fullscreen/Details) and roughly half-width (split), crossing the rerender margin with no resize or fullscreen toggle involved — does not kill+restart a session for every row a fast multi-row traversal passes through. The first evaluation of a newly-bound task past the margin SHALL arm a pending kick (recording the task and its current width) rather than firing; only a LATER evaluation, once the dwell has elapsed AND the same task is still the bound target, SHALL actually invoke the kick. A rebind to a DIFFERENT task before the dwell elapses SHALL discard the prior pending kick un-fired and arm fresh against the new task. The dwell SHALL be evaluated without a new goroutine or timer, on the same Draw-driven cadence `maybeKickPaneRerender` already runs on.

Derived from: `internal/tui/hera/panes.go:86` (`bindPane` ForceResyncPTY), `internal/tui/hera/panes.go:153` (`SyncPanes`), `internal/tui/hera/panes.go:172` (`forwardKey` main-thread-safe reads), `internal/tui/hera/panes.go` (`maybeKickPaneRerender`, called from `page.go`'s `Draw` right after each pane's `SetRect` — not from `bindPane`, since a pane hidden by details mode has no real width yet at bind time; now also gated by a `kickPending` dwell), `internal/tui/app.go` (`maybeKickRerenderAtWidth`, `heraKickRerender`, `HeraPage.SetRerenderKicker`), `internal/agent/rerender.go` (`ShouldKickRerender`, `RerenderMargin`), `context/knowledge/gotchas/hera-view.md` (BUG-074, and the kick-debounce bullet added by this change), `context/knowledge/gotchas/pty-terminal.md`.

#### Scenario: Bind resizes a full-width session down

- **WHEN** a session sized for the full-width agent view is bound into a hera pane
- **THEN** `ForceResyncPTY` arms an unconditional resize and `SyncPanes` applies it off the main thread

#### Scenario: Off-tab SyncPanes is a no-op

- **WHEN** `SyncPanes` is called while the Hera tab is not active
- **THEN** no resize fires (panes not drawn this frame have zero pending resize), so it cannot fight the main agent view's resize of the same task

#### Scenario: Binding a session with drifted committed width kills and resumes it, after a dwell

- **WHEN** the coordinator pane or the worker/agent pane binds a live, idle, resumable session whose `InitialPTYSize` cols differ from the pane's current cols by at least the rerender margin, no kick is already pending for that task, and the SAME task remains bound for at least the debounce dwell
- **THEN** the session is stopped and the existing exit-handler resumes it via `--session-id` at the pane's current dimensions, so its scrollback re-renders at the current width instead of staying corrupted at the old one

#### Scenario: A busy or prompt-blocked session is not killed on bind

- **WHEN** a bind's width drift meets the rerender margin but the session is not idle, or is idle only because it is blocked on a user prompt
- **THEN** the session is left running (no kick), so an in-flight tool call or an `AskUserQuestion` overlay is never interrupted

#### Scenario: A redundant rebind at the same width does not re-evaluate the kick

- **WHEN** a task is rebound into a hera pane at a width already evaluated for that task (whether the prior evaluation was from the same pane, the other hera pane, or the main agent view)
- **THEN** the kick predicate is skipped for that rebind

#### Scenario: A backend without a resumable session ID is never kicked

- **WHEN** a bound task's session has no resumable session ID (e.g. a Codex-backed task)
- **THEN** no kick is attempted regardless of width drift

#### Scenario: A fast multi-row rail traversal never kicks any of the transiently-bound tasks

- **WHEN** the rail cursor moves across several rows in quick succession (each hop rebinding a different task past the rerender margin), and no single task stays bound for the full debounce dwell
- **THEN** none of the transiently-bound tasks are kicked — each hop's pending kick is discarded, un-fired, by the next hop's rebind

#### Scenario: A genuine dwell-and-stay still kicks, just later

- **WHEN** the rail cursor lands on a row and stays there past the debounce dwell, and the bound task's width drift still meets the rerender margin at that point
- **THEN** the kick fires exactly once, ~300ms after the bind rather than immediately

### Requirement: Debounced rail refresh on the UI thread (area 6)

The system SHALL rebuild the rail model via a goroutine-free, timer-free debounced `Refresher` driven by the app tick and tab entry. `Schedule()` coalesces bursts into one rebuild per debounce window; tab entry forces an immediate flush. Rebuilds run on the tview thread because hera-store reads are mutex-guarded and fast (the "never on the UI thread" rule is about git, not DB reads). After `SetModel` the selection is re-derived and the panes rebound, so stale model pointers are refreshed.

Within the debounce window's rebuild opportunity, the system SHALL additionally skip the actual `BuildModel`+`SetModel` rebuild work when a cheap change-detection check proves nothing that could affect the rendered rail has changed since the last rebuild: a SQLite `PRAGMA data_version` fingerprint of the underlying store (unchanged since the last rebuild) AND all four per-tick runtime maps fed into the model (`needsInput`, `sessionIdle`, `sessionRunning`, `sustainedActive`) equal to their values at the last rebuild. The very first rebuild opportunity SHALL always run (no prior snapshot to compare against). A store that does not expose a data-version fingerprint (remote mode's nil reader; a test double) SHALL always be treated as changed, so the gate never suppresses a rebuild it cannot prove is safe to skip. Because a write made through the SAME store connection that performs the tick's own reads does not change what that connection's own `PRAGMA data_version` read reports (a documented SQLite same-connection blind spot), every interactive hera mutation's existing immediate-refresh path SHALL also invalidate the cached fingerprint, so the tick immediately following any such mutation always takes the full rebuild path regardless of what the fingerprint reports.

When the App has already fetched the full task list this tick (it always has, for the plain task list), the Hera model rebuild SHALL reuse that snapshot rather than performing a second, redundant full task-list fetch of its own; a rebuild opportunity for which no such snapshot has been supplied SHALL fetch it itself, unchanged from today.

Derived from: `internal/tui/hera/refresher.go` (`Refresher`), `internal/tui/hera/page.go:138` (`ScheduleRefresh`), `internal/tui/hera/page.go:150` (`doRefresh`, now gated by `shouldRebuild`/`markRebuilt`), `internal/tui/hera/panes.go:59` (`applySelection` re-run), `internal/db` (`DB.DataVersion`), `internal/tui/heraactions.go` (`heraRefresh` fingerprint invalidation), `context/knowledge/gotchas/hera-view.md`.

#### Scenario: Burst of writes coalesces to one rebuild

- **WHEN** several store writes schedule refreshes within one debounce window
- **THEN** the rail rebuilds once

#### Scenario: Tab entry forces a fresh rail

- **WHEN** the Hera tab is opened
- **THEN** the refresher flushes immediately so the rail is current the instant the tab appears

#### Scenario: A quiescent tick skips the rebuild entirely

- **WHEN** a rebuild opportunity arrives (debounce window elapsed) and neither the store's data-version fingerprint nor any of the four runtime maps has changed since the last rebuild
- **THEN** `BuildModel`/`SetModel` are not called; the rail keeps its last-built model unchanged

#### Scenario: A DB-only change (no runtime-map change) still triggers a rebuild

- **WHEN** the store's data-version fingerprint has changed since the last rebuild (e.g. a daemon-driven hera binding write) but none of the four runtime maps differ
- **THEN** the rail rebuilds

#### Scenario: A runtime-only change (no DB write) still triggers a rebuild

- **WHEN** the data-version fingerprint is unchanged but at least one of `needsInput`/`sessionIdle`/`sessionRunning`/`sustainedActive` differs from the last-rebuild snapshot (e.g. an active agent produced output or went idle)
- **THEN** the rail rebuilds, so the spinner/needs-input glyphs never freeze while the underlying DB rows are stable

#### Scenario: A local hera mutation is never missed by the gate

- **WHEN** a TUI-side hera mutation (pin, status step, kanban step, hide, spawn, nuke) writes through the same store connection the tick reads from, and its handler calls the existing immediate-refresh path
- **THEN** the cached fingerprint is invalidated as part of that path, so the next rebuild opportunity takes the full rebuild regardless of whether the fingerprint alone would have reported a change

#### Scenario: A store without a data-version fingerprint always rebuilds

- **WHEN** the reader does not implement the fingerprint (remote mode, or a test double)
- **THEN** every rebuild opportunity runs the full `BuildModel`/`SetModel` pass, identical to behavior before this change

#### Scenario: The Hera model rebuild reuses a supplied task snapshot instead of re-fetching

- **WHEN** the App has already fetched the task list this tick and supplies it to the Hera rebuild
- **THEN** the rebuild uses that snapshot and performs no second underlying task-list fetch of its own
