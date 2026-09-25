## Why

**The TUI's per-second needs-input/content-idle scan is O(concurrently-running
sessions), with no ceiling.** Investigated live after the `argus` process
showed sustained high CPU in Activity Monitor: the user regularly runs dozens
of concurrent Claude/Codex sessions under Argus (49 observed live). Every
1-second tick, `detectNeedsInputSticky` + `agent.ContentIdle`
(`internal/tui/app.go`, invoked from `refreshTasksWithIDs` inside
`QueueUpdateDraw` on the tview main goroutine) read a 16KB log tail and run a
full VT terminal re-emulation pass for every running session whose on-disk log
changed since the previous tick. The existing `logUnchanged` Stat()-based
dirty check (dedupe-redundant-needsinput-reads) already skips this for a
session that hasn't written anything, but a session that IS actively
streaming — which, across a large fleet, is most of them most of the time —
still pays the full cost every single tick. The per-tick cost scales linearly
with fleet size with no cap, so a heavy multi-tasking workflow (dozens of
concurrent worktrees/sessions) turns a per-tick cost that's negligible for a
handful of tasks into a real, continuous CPU tax, synchronously inside the UI
goroutine's `QueueUpdateDraw` callback.

This is the first of several scalability passes requested to keep Argus
responsive as concurrent session count grows — see Non-Goals below for what's
intentionally deferred to a follow-up.

## What Changes

- **The needs-input/content-idle fleet scan is now batched above a fixed
  threshold (`needsInputScanBatchSize = 24`).** At or below the threshold,
  every running session is scanned in full every tick — byte-identical to
  today's behavior, so a typical/small fleet sees zero change. Above it,
  running sessions are partitioned into fixed-size rotation batches (sorted
  for a stable assignment across ticks); exactly one batch is "due" for a
  fresh tail-read + re-emulation per tick, and the rest reuse ("replay") their
  last-computed raw signal — the SAME replay mechanism the existing
  `logUnchanged` dirty check already uses, just gated by an additional
  "is this session's batch in rotation this tick" condition.
- **Every tick-scoped counter still advances every tick, for every running
  session, regardless of batch membership** — only the expensive
  read+re-emulation is gated, never the counter-stepping logic the existing
  BUG-029/060/061/065/072 fixes depend on running once per tick.
- **A session's first-ever observation is never delayed by rotation** — a
  session with no prior cached raw signal is always treated as due, so a
  freshly-spawned session's first reading happens on its first tick exactly
  as before.
- **Net effect:** the worst-case per-tick scan cost is now bounded at
  `needsInputScanBatchSize` sessions regardless of how large the fleet grows,
  at the cost of up to one rotation's worth of ticks (bounded, self-healing,
  never a permanent miss) of staleness for an out-of-batch session — the same
  trade-off class already accepted elsewhere in this codebase (the dirty
  check itself, BUG-060's one-tick escalation grace, the macOS notification
  flood gate).

## Non-Goals (this change)

Named follow-ups for the next scalability iteration, not silently dropped:

- **`spinnerLoop`'s forced-redraw gate is still fleet-wide** (any running,
  non-idle session anywhere triggers a 100ms-cadence `QueueUpdateDraw`,
  regardless of whether that session's spinner is currently on screen). With
  a large fleet this makes the periodic-redraw path fire almost continuously
  instead of only while something visible is animating. Left for a follow-up
  change since it touches redraw semantics rather than the detection state
  machine this change is scoped to.
- **`refreshTasksWithIDs`'s heavy computation (needs-input/content-idle scan,
  Hera role reads, PR-state reads) still runs synchronously inside
  `QueueUpdateDraw` on the tview main goroutine**, not moved off it. This
  change reduces the WORK done there for a large fleet but doesn't change
  WHERE it runs.

## Capabilities

### Added Capabilities

- `idle-detection`: the per-tick needs-input/content-idle fleet scan is now
  batched above a fixed threshold, capping worst-case per-tick scan cost
  independent of fleet size.

## Impact

- **Modified code:**
  - `internal/tui/app.go` — `needsInputScanBatchSize` constant,
    `needsInputScanBatch` (rotation-batch predicate), `App.needsInputScanCursor`
    field, `detectNeedsInputSticky`'s `reuseCached` helper (replaces the 3
    direct `logUnchanged` call sites in the content-stability,
    resumed-activity, and settlement passes).
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  in-memory TUI-side scheduling change; the daemon's own idle watcher
  (`internal/push`) and the REST-exposed idle state are untouched.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
