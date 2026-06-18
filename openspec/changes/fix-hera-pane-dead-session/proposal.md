## Why

BUG-013: a Hera coordinator (and possibly agent) pane intermittently "locks up"
— the operator can no longer type into the agent, while global keys (rail nav,
`1` to the Tasks tab) still work. Restarting the TUI fixes it. Strongly
correlated with the coordinator finishing work / its task being marked complete.

Root cause: the pane holds a stale `RemoteSession` whose stream the daemon tore
down (a StreamLost relay or a daemon bounce) WHILE the agent PTY is still alive,
so `RemoteSession.Alive()` flips false. The pane is never re-resolved:

- `reconcileOne` bailed whenever the pane already held ANY session (`tp.Session()
  != nil`), so it only ever late-bound a nil session — never replaced a dead one.
- `bindPane` no-ops on an unchanged taskID, so a fresh selection of the same role
  doesn't help either.
- `forwardKey` dropped the keystroke SILENTLY when the session was nil or
  `!Alive()` — no uxlog, so the freeze was invisible in the logs.

A full TUI restart re-dials a fresh stream → `Alive()` true again → input works.
That is why "restart fixes it."

## What Changes

- `reconcileOne` re-resolves a present-but-DEAD pane session (not just a nil one):
  when the bound session is `!Alive()`, ask the provider for a fresh handle and
  swap it in. The daemon client re-dials a new stream on a cache-miss `Get` when
  the daemon still reports the process alive, so the pane becomes interactive
  again on the next tick — no TUI restart. A dead handle is replaced ONLY by a
  genuinely live, DIFFERENT handle; when no live replacement exists (process
  really gone → on-disk replay; or the same not-yet-evicted handle → retry next
  tick) the pane is left untouched so the emulator is never needlessly reset.
- `forwardKey` no longer drops a keystroke silently: it logs the drop (prefix
  `[hera]`), attempts an immediate re-resolve, and retries the write on the fresh
  handle so the keystroke is not lost.
- This is orthogonal to the ctrlz-reattach `Enter`-revive: that path deliberately
  leaves a LIVE coordinator navigate-only and restarts a truly DEAD session's
  process. BUG-013 targets a live process whose pane handle merely went stale, so
  re-resolution (re-dial the stream) — not a process restart — is the fix.

## Capabilities

### Modified Capabilities

- `hera-view`: A fed pane holding a present-but-dead session is re-resolved to a
  fresh live handle on the tick and on the next keystroke, and a dropped
  keystroke is logged rather than silently swallowed.

## Impact

- **Modified code:** `internal/tui/hera/panes.go` (`reconcileOne` dead-handle
  re-resolution, `forwardKey` log + re-resolve, `paneBinding` helper).
- **Tests:** `internal/tui/hera/panes_test.go` (dead→live replacement on
  reconcile + on forwardKey; dead handle retained when no live replacement).
- **Docs:** `context/knowledge/gotchas/hera-view.md` (the dead-handle invariant).
- **No new keys** (no help-overlay/README change), **no schema change, no daemon
  RPC, no `screen.Sync()`** — this is input routing, not rendering. Specs stay
  LOCAL DOCS only; gate stays `make pre-pr`.
