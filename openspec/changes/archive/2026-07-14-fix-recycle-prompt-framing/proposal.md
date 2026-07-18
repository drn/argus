## Why

`BuildRecycleSeedPrompt` (`internal/hera/recycle.go`) concatenates the role's
original mission text, then a framing sentence, then the current plan-DAG
state, then any handoff note. A real recycled coordinator session (on an
unrelated project) anchored on the original mission as its live instruction
and started re-doing already-completed work, even though the plan-DAG state
showed 4/4 nodes done/idle and the handoff note said "no blocking decisions
pending." Root cause: the disambiguating framing sentence arrives AFTER the
mission text, so the model has already anchored on the mission as "the task"
by the time the framing arrives — and the mission itself is never marked as
historical, so it reads exactly like a live instruction (because it was one,
just a stale one).

The base spec (`coordinator-context-management`, "recycle_coord restarts a
coordinator on its existing task without losing its place") requires the seed
prompt to be assembled from the mission, plan-DAG state, and handoff note, but
says nothing about how a fresh session should weigh the (now possibly stale)
mission against the current state — the gap that let this ordering ship
without the anchoring risk being caught.

## What Changes

- `BuildRecycleSeedPrompt` explicitly marks the original mission text as
  historical background before showing it, and states up front that the
  current plan-DAG state and handoff note (which follow) supersede it — so a
  fresh coordinator reads "what's actually going on" before "what I was
  originally asked," rather than the reverse.
- No change to what data is included (mission, plan-DAG state, handoff note)
  or how it's gathered — this is a framing/ordering fix to the prompt text
  only, not a new capability.

## Capabilities

### Modified Capabilities

- `coordinator-context-management`: the "recycle_coord restarts a coordinator
  on its existing task without losing its place" requirement gains an
  explicit sentence requiring the original mission to be marked historical/
  superseded, with the current-state framing preceding it in the assembled
  text; a new scenario asserts the mission text is clearly marked as
  historical in the seed prompt.

## Impact

- **Code:** `internal/hera/recycle.go` (`BuildRecycleSeedPrompt` only).
- **Tests:** `internal/hera/recycle_test.go` — extend the existing
  `TestBuildRecycleSeedPrompt_ComposesMissionPlanStateAndHandoffNote` case (or
  add a sibling) asserting the historical-marking text precedes the mission
  and the state/handoff framing is up front.
- **Docs:** none beyond the spec delta — this is a narrow prompt-text fix, not
  a new invariant worth a gotcha bullet.
- **No schema, MCP tool, REST, TUI, or macOS surface change** — this only
  touches the text handed to the fresh session's first turn.
