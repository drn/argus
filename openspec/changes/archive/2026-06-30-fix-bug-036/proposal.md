## Why

**BUG-036 — a fullscreen (alt-screen) agent parked at its prompt renders a perpetual spinner instead of an idle indicator, and idle-push never fires for it.**

`Session.IsIdle()` is based on `lastOutput` — the wall-clock of the last raw PTY
byte. A fullscreen Claude agent repaints its screen continuously (cursor blink,
spinner timing line, alt-screen redraws), so raw bytes never quiesce →
`IsIdle()` is never true → the session never enters the runner's idle set. Two
consequences:

- **The Hera rail spinner never stops.** `RoleView.IsActive()` (the honest
  "working" signal that animates the rail/plan spinner) is `Live &&
  task in_progress`, with no idle awareness — so a parked fullscreen worker
  spins forever even though it is doing nothing.
- **idle-push never fires for fullscreen agents.** The idle-push transition is
  gated on the raw idle set, which a fullscreen agent never joins.

The needs-input path already worked around this for the *prompt* case
(BUG-032/033/035 never-idle content-stability passes), but a parked agent that
is simply idle at its input box (no pending question) is caught by neither — it
just spins.

## What Changes

- **Introduce a content-aware idle signal.** A session is "content-idle" when its
  animation-stripped EMULATED-screen fingerprint (the BUG-033 `ScreenRenderer` +
  `fingerprintText` machinery, sized via the session-size sidecar) has been
  UNCHANGED for ~`idleThreshold` AND Claude's "working" affordance (`esc to
  interrupt`) is ABSENT — even though raw bytes keep flowing as repaint
  animation. Computed off the hot paint path, on the existing watcher/TUI ticks.
- **Feed content-idle into the Hera spinner.** `RoleView.IsActive()` additionally
  requires the session NOT be content-idle, so a parked fullscreen agent shows
  the live/idle moon glyph (or `(?)` if at a prompt, which already outranks the
  spinner), NOT a spinner. A genuinely content-ACTIVE agent (emulated content
  changing, or showing the interrupt affordance) still spins.
- **Feed content-idle into idle-push + `session.idle`.** The daemon folds the
  content-idle set into the idle set used for the busy→idle transition, so a
  fullscreen agent fires idle-push once when its content stabilizes. The existing
  per-work-cycle gate (`shouldFireIdlePush`: no re-push until new input arrives)
  guarantees EXACTLY-ONCE firing — a flapping content signal cannot storm, and
  non-fullscreen agents (already raw-idle) are unaffected.
- **`Session.IsIdle()` itself is deliberately left raw-byte-based.** Content-idle
  is a parallel, tick-computed signal (it is inherently stateful — fingerprint
  history + a stability timer + an emulator — and `IsIdle()` is a stateless,
  cross-process, widely-consumed predicate). This mirrors the existing dual
  daemon/TUI needs-input detector rather than mutating the shared method, keeping
  the blast radius contained (reliable-pane delivery, the resize-kick gate, and
  the supervisor RPC all keep their raw-idle semantics).

## Capabilities

- `idle-detection` — content-aware idle classification + once-on-transition idle-push.
- `hera-view` — the rail/plan spinner reflects content-aware idle, not just task status.

## Out of scope

- The task-list spinner (`TaskStatusIcon`) shares the same raw-idle root but is
  left unchanged to keep the fix surgical; the Hera rail is the reported surface.
- `Session.IsIdle()` semantics, reliable-pane delivery gating, and the
  `/api/tasks` idle flag are unchanged.
