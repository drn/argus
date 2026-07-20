## Why

**BUG — `C` (clear-this-coordinator's-archive) strands a hidden sub-coordinator
as a new top-level root instead of removing it.**

Hiding (`a`) a sub-coordinator collapses its bridging worker row into the
parent's archive expando WITHOUT ending its binding (archiving only stamps
`archived_at`; per the base spec's own Tier-1 definition it "keeps the
worktree and session alive"). `C`'s sweep (`Model.SubtreeArchivedWorkers`)
finds that hidden bridging row and treats it like any other flat leaf worker:
it ends only that ONE binding via `Ops.NukeRole`. The child orchestrator's own
row is untouched and keeps its own coordinator role, bindings, and any further
nested children — with no parent link left, it renders as a brand-new
top-level root on the very next rail rebuild instead of disappearing along
with the rest of the archive.

Reported repro: pressing `C` on a coordinator with several archived
sub-coordinators (each with its own nested tasks) killed the sub-coordinators'
*workers* but dumped every sub-coordinator itself back onto the rail as a
separate top-level entry — the opposite of "clear the archive."

`Ctrl+D` already handles the equivalent LIVE case correctly (`heraOpenDelete`
checks `BridgeChildOrchID` and redirects into `heraCascadeNukeFrom`, tearing
down the whole nested subtree) — but that check only fires for the row
currently under the cursor. `C` walks the WHOLE subtree looking for hidden
rows, so it can find an archived bridging row anywhere beneath the selected
coordinator, and had no equivalent cascade for what it finds.

## What Changes

- **`C` now cascade-deletes the full nested subtree behind every ARCHIVED
  bridging row it finds, in addition to ending that row's own binding.**
  Ending the bridging role's own binding (the pre-fix behavior) stays correct
  and unchanged; the new part is the child orchestrator (and anything nested
  beneath IT) is also fully nuked in the same `C` action — matching the
  Ctrl+D-on-a-live-bridge semantics, applied to the archived case.
- **New pure model helper `Model.SubtreeArchivedBridges(orchID) []int64`**
  finds the child orchestrator IDs behind every archived bridging worker role
  in the subtree (companion to the existing `SubtreeArchivedWorkers`, which is
  unchanged).
- **The `C` confirm message reports both halves**: the flat hidden-agent count
  (unchanged wording) plus, when non-empty, how many nested sub-team(s) —
  and how many orchestrators/agents/worktrees within them — are also being
  fully removed.
- **A new shared helper `countCascadeSubtree`** factors the agent/worktree/
  preserved tally out of `heraCascadeNukeFrom` (Ctrl+D) so `C`'s new message
  can reuse the identical counting logic rather than re-deriving it.
- **(post-review fix) The cascade sub-message's counts are now accurate even
  though `C` ends the bridging role's own binding BEFORE running the cascade.**
  `heraTaskBoundOutside`/`countCascadeSubtree` gained an `excludeRoleIDs` param;
  `heraClearArchive` excludes `workers`' role IDs so a shared bridge task isn't
  previewed as "preserved" when it is actually about to be reclaimed by the
  cascade that runs right after in the same action. `Ctrl+D` passes `nil` —
  unaffected.

## Capabilities

### Modified Capabilities

- `hera-view`: `C` (clear-this-coordinator's-archive) now fully cascade-deletes
  the nested subtree behind an archived bridging (sub-coordinator) row, not
  just that row's own binding — closing the gap where a hidden sub-coordinator
  reappeared as a new top-level orchestrator instead of being removed.

## Impact

- **Modified code:**
  - `internal/tui/hera/eol.go` — new `SubtreeArchivedBridges` pure helper;
    `SubtreeArchivedWorkers` doc updated (behavior unchanged).
  - `internal/tui/heraactions.go` — `heraClearArchive` additionally cascades
    every archived bridge's child subtree via `heraDoCascadeNuke`;
    `heraCascadeNukeFrom`'s inline tally extracted into `countCascadeSubtree`
    and reused by both callers.
- **No new key, no schema change, no daemon RPC.** Pure TUI-side mutation
  logic; reuses the existing NUKE primitives (`Ops.NukeRole`,
  `Ops.NukeOrchestrator`, `heraReclaimAndArchiveTask`) exactly as `Ctrl+D`
  does — zero hard deletes, same DB-recoverable NUKE semantics.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
