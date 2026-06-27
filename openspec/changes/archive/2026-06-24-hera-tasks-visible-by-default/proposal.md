## Why

Hera-managed tasks (spawned workers + tasks holding a live coordinator/worker
binding) are hidden from the Tasks tab by default — `hideHeraManaged` defaults to
`true` (`internal/tui/taskview/tasklist.go:148`), and the row-build filter drops
them (`tasklist.go:390`). The `H` key reveals them. The rationale was "they have a
home in the Hera tab," but in practice this makes a chunk of live work invisible on
the primary navigation surface: a user scanning the Tasks tab can't see that a
coordinator spawned three workers, and has to remember to press `H`.

We want hera-managed tasks **visible inline by default**, distinguished by a
per-row indicator, with `H` still available to hide them for a clean plain-tasks
view. Coordinators already render a cyan indicator glyph
(`theme.IconCoordinator`, `tasklist.go:1283`); workers render none. Making them
visible by default only reads well once workers are also visually marked.

## What Changes

- **Flip the default**: `hideHeraManaged` defaults to `false` — hera-managed tasks
  (workers + coordinators) are shown inline by default. The single `H` toggle is
  unchanged in mechanism: pressing it hides every hera-managed task, pressing again
  reveals them. Only the initial state flips.
- **Add a worker indicator glyph.** A task row that is a hera-spawned worker (or
  holds a live worker-kind binding) renders a distinct indicator in the same
  indicator lane as the existing coordinator glyph, so a visible hera task is
  identifiable at a glance without opening the Hera tab. New `theme.IconWorker` /
  `theme.StyleWorker`; coordinator glyph unchanged.
- **Indicator precedence**: a task holding a coordinator role keeps the coordinator
  glyph (coordinator outranks worker if somehow both apply); a worker-only task
  gets the worker glyph; freelancers and plain tasks get neither — unchanged.

## Capabilities

### Modified Capabilities

- `task-list-view`: the hera-visibility toggle defaults OFF (hera-managed tasks
  visible inline by default); a per-row worker indicator is added alongside the
  existing coordinator indicator.

## Impact

- **Modified code:** `internal/tui/taskview/tasklist.go` (default `hideHeraManaged`
  → false; worker glyph in `drawTaskRow`), `internal/tui/theme/theme.go` (add
  `IconWorker` / `StyleWorker`).
- **Tests:** `internal/tui/taskview/hera_workers_test.go` (default now shows
  workers; `H` hides; worker glyph rendered; coordinator-outranks-worker), render
  assertion for the worker indicator cell.
- **Docs:** `context/knowledge/gotchas/tasklist-ui.md` (default flipped; worker
  indicator cell + precedence vs coordinator/PR glyphs), README Reference (note `H`
  now *hides* rather than *reveals* by default; add the worker indicator to the
  glyph legend if present).
- **Help modal:** the `H` entry text ("show/hide hera-managed (workers+coords)")
  stays accurate — no key added/removed, so `help_test.go` is unchanged unless the
  glyph legend is asserted.
- **No new keys, no schema change, no daemon RPC, no `screen.Sync()`.** Specs stay
  LOCAL DOCS only; the gate stays `make pre-pr`.
