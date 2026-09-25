## Why

Named follow-up from `add-needs-input-fleet-batching` (PR #1016): `spinnerLoop`
(`internal/tui/app.go`) drives a 100ms-cadence forced `QueueUpdateDraw` redraw
whenever ANY running-and-not-idle session exists ANYWHERE in the whole fleet
— not just one whose spinner glyph is actually rendered on screen. With a
large concurrent fleet (dozens of sessions), it's virtually always true that
*something* is non-idle, so this periodic-redraw path fires almost
continuously instead of intermittently, adding a sustained, fleet-size-
correlated CPU floor to the `argus` process even though the gate's own doc
comment says its purpose is exactly to avoid that ("Skipping redraws when all
tasks are idle prevents unnecessary full-screen repaints... and waste CPU").

## What Changes

- **`spinnerLoop`'s redraw gate now asks whether a spinner actually rendered
  on screen would animate**, not merely "does anything anywhere exist". A new
  `computeVisibleActiveSpinner` recomputes this once per `refreshTasksWithIDs`
  call (tview main goroutine) and caches the result into `App.visibleActiveSpinner`
  (guarded by the existing `a.mu`), which `spinnerLoop`'s background goroutine
  reads instead of touching `a.tasklist`/`a.header` directly — those are tview
  widgets, unsafe to read outside the tview main goroutine.
- **Scoped only for the base Tasks-tab view** (`modeTaskList` +
  `widget.TabTasks`), where `TaskListView.VisibleTaskIDs()` (an existing,
  already-tested inspection seam) cheaply exposes the filtered row set really
  on screen. A running-but-idle or running-but-filtered-out/hidden task no
  longer keeps the periodic redraw alive.
- **Every other mode falls back to the original fleet-wide check, byte-
  identical to before** — the Hera tab, the fullscreen agent view, and every
  modal/picker. Deliberately not scoped in this pass: the Hera rail's spinner
  rendering has an extensive, documented history of subtle precedence bugs
  (BUG-036/BUG-C/BUG-F and others in `gotchas/hera-view.md`), and the
  fullscreen agent view's own PTY rendering is driven independently by real
  output, not this gate — touching either without dedicated, thorough testing
  would risk a new regression for a use case (Hera-heavy fleets) this
  investigation didn't confirm is even the dominant cost driver here.
- **Bounded, self-healing staleness**: the cached gate can lag a tab switch by
  up to one ~1s tick (the next `refreshTasksWithIDs` call recomputes it) —
  the same trade-off class as `add-needs-input-fleet-batching`'s rotation
  staleness.

## Non-Goals (this change)

- Hera-tab / agent-view / modal spinner-redraw scoping — named, deferred, not
  silently dropped. Revisit if a live investigation shows the Hera tab is the
  dominant redraw-frequency driver for a given user's fleet shape.
- The sibling follow-up ("move `refreshTasksWithIDs`'s heavy computation off
  the tview main goroutine") was investigated in the same session and found
  NOT safely implementable as a small change — see the session record / PR
  description for the two concrete failure modes found (a data race against
  `Draw()` via `a.tasks` pointer sharing for the needs-input scan path, and a
  demonstrated stale-read bug for a DB-read caching approach). Left
  unimplemented by deliberate judgment call, not oversight.

## Capabilities

### Modified Capabilities

- `tui-shell`: the spinner-animation redraw gate is now scoped to visible
  work on the base Tasks-tab view instead of the whole fleet.

## Impact

- **Modified code:** `internal/tui/app.go` — `App.visibleActiveSpinner` field,
  `computeVisibleActiveSpinner`, `spinnerLoop`'s gate, one cache-write line at
  the end of `refreshTasksWithIDs`.
- **No new key, no new dependency, no schema change, no daemon RPC.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
