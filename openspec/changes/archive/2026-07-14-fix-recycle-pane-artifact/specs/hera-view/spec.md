## MODIFIED Requirements

### Requirement: A present-but-dead pane session is re-resolved, not just a nil one (area 6)

The system SHALL, on every tick reconcile and on every keystroke into a focused
pane, treat a pane that holds a session whose `Alive()` is false the same as an
unbound pane: it SHALL re-resolve the live session via the `SessionResolver`
seam and swap in a fresh handle, so a pane whose stream the daemon tore down
(StreamLost relay / daemon bounce) WHILE the agent process is still alive becomes
interactive again WITHOUT a full TUI restart. A dead handle SHALL be replaced
ONLY by a genuinely live handle whose pointer differs from the dead one; when the
resolver yields nothing (the process is really gone, so on-disk log replay backs
the pane) or the SAME not-yet-evicted handle (the client cache has not evicted
it; re-resolve on a later tick), the pane SHALL be left untouched so the emulator
is not needlessly reset. A live, present session SHALL be left alone (this never
restarts or navigates a live coordinator — that remains the `Enter`-reattach
path's responsibility).

Every genuine swap onto a different session handle (this reconcile
replacement, and `bindPane`'s task-changed rebind) SHALL reset the pane's
VT/replay state (`ResetVT`) before attaching the new handle, so no emulator
state, cached replay content, or scroll anchor left over from the outgoing
session can survive into the incoming session's render — the same
`SetTaskID→ResetVT→SetSession` order the main (non-Hera) agent view already
follows on every task/session transition.

Derived from: `internal/tui/hera/panes.go` (`reconcileOne` dead-handle branch, `bindPane`, `reconcileSessions`, `paneBinding`), `internal/daemon/client/client.go` (`Get` re-dials on a cache-miss when the daemon reports the process alive).

#### Scenario: A dead pane session is replaced on the next tick

- **WHEN** a pane holds a session that has gone `!Alive()` and the provider can resolve a fresh live handle for the same task
- **THEN** the next reconcile swaps the fresh handle in (re-armed PTY resize) and the pane is interactive again

#### Scenario: A dead session with no live replacement is retained

- **WHEN** a pane holds a `!Alive()` session and the provider returns nil (process gone) or the same dead handle (cache not yet evicted)
- **THEN** the pane keeps its current handle (its buffered output still backs log replay) and is not reset

#### Scenario: A same-task session swap (e.g. `recycle_coord`) leaves no stale render state

- **WHEN** a pane's bound task is recycled — the session dies and a fresh, distinct live handle for the SAME task ID is resolved on a later reconcile
- **THEN** the swap resets the pane's VT/replay state before attaching the fresh handle, so the fresh session's render cannot show any cell or replay content carried over from the outgoing session
