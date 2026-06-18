# Hera View

## ADDED Requirements

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

Derived from: `internal/tui/hera/panes.go` (`reconcileOne` dead-handle branch, `reconcileSessions`, `paneBinding`), `internal/daemon/client/client.go` (`Get` re-dials on a cache-miss when the daemon reports the process alive).

#### Scenario: A dead pane session is replaced on the next tick

- **WHEN** a pane holds a session that has gone `!Alive()` and the provider can resolve a fresh live handle for the same task
- **THEN** the next reconcile swaps the fresh handle in (re-armed PTY resize) and the pane is interactive again

#### Scenario: A dead session with no live replacement is retained

- **WHEN** a pane holds a `!Alive()` session and the provider returns nil (process gone) or the same dead handle (cache not yet evicted)
- **THEN** the pane keeps its current handle (its buffered output still backs log replay) and is not reset

### Requirement: A dropped pane keystroke is logged, not silently swallowed (area 6)

The system SHALL, when a keystroke is forwarded to a focused pane whose session
is nil or `!Alive()`, emit a uxlog line (rather than dropping silently), attempt
an immediate re-resolve of the pane's session, and retry the write on the
re-resolved handle so the keystroke is not lost when a fresh live session is
available; if no live session can be resolved, it SHALL log that the keystroke
was dropped.

Derived from: `internal/tui/hera/panes.go` (`forwardKey` re-resolve + uxlog).

#### Scenario: Keystroke into a dead pane re-resolves and is delivered

- **WHEN** a key is forwarded to a pane whose bound session went `!Alive()` and a fresh live handle is resolvable
- **THEN** the pane re-resolves the fresh handle and the keystroke is written to it

#### Scenario: Keystroke into a pane with no live session is dropped with a log line

- **WHEN** a key is forwarded to a pane with no live session and none can be resolved
- **THEN** the keystroke is dropped, a uxlog line records the drop, and nothing is written to the dead handle
