## MODIFIED Requirements

### Requirement: PTY size alignment on bind (area 6)

The system SHALL resize a bound session to the (narrower) hera pane when binding it, by calling `ForceResyncPTY()` so a session previously sized for the full-width main agent view is resized down on the next Draw, with `SyncPanes()` issuing the Resize RPC off the main thread. Pane operations on the tview thread SHALL use only lock-free/local session methods; the blocking resize RPC runs only from the tick goroutine. The view SHALL NOT use `screen.Sync()` to paper over a size mismatch.

A plain PTY resize (SIGWINCH) only re-flows a session's LIVE UI — it cannot repair scrollback already committed at a different width, because cursor-positioning codes baked into earlier PTY output remain wrong once re-emulated at a new size. When binding a session (coordinator pane or worker/agent pane) whose recorded initial PTY width differs from the current hera pane's width by at least the shared rerender margin, the system SHALL evaluate the SAME kill+resume decision the main agent view applies on entry (`agent.ShouldKickRerender`), using the hera pane's own current width — not the main agent view's. The decision SHALL be skipped when a kick is already pending for the task, when the session lacks a resumable session ID, when the agent is not idle (deferred, not lost), or when the agent is blocked on a user prompt (deferred, never dismissed). The redundant-attach cache SHALL be shared with the main agent view's (keyed by task ID, not by which surface is asking), so a task already evaluated at its current attach width is not re-evaluated on every pane rebind.

Derived from: `internal/tui/hera/panes.go:86` (`bindPane` ForceResyncPTY), `internal/tui/hera/panes.go:153` (`SyncPanes`), `internal/tui/hera/panes.go:172` (`forwardKey` main-thread-safe reads), `internal/tui/app.go` (`maybeKickRerenderAtWidth`, `heraKickRerender`, `HeraPage.SetRerenderKicker`), `internal/agent/rerender.go` (`ShouldKickRerender`, `RerenderMargin`), `context/knowledge/gotchas/hera-view.md`, `context/knowledge/gotchas/pty-terminal.md`.

#### Scenario: Bind resizes a full-width session down

- **WHEN** a session sized for the full-width agent view is bound into a hera pane
- **THEN** `ForceResyncPTY` arms an unconditional resize and `SyncPanes` applies it off the main thread

#### Scenario: Off-tab SyncPanes is a no-op

- **WHEN** `SyncPanes` is called while the Hera tab is not active
- **THEN** no resize fires (panes not drawn this frame have zero pending resize), so it cannot fight the main agent view's resize of the same task

#### Scenario: Binding a session with drifted committed width kills and resumes it

- **WHEN** the coordinator pane or the worker/agent pane binds a live, idle, resumable session whose `InitialPTYSize` cols differ from the pane's current cols by at least the rerender margin, and no kick is already pending for that task
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
