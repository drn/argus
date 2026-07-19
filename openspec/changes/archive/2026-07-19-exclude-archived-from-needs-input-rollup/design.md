## Context

The Hera rail already propagates a "needs-input" (`?`) signal up the orchestration
tree to the root coordinator (`openspec/specs/hera-view/spec.md` requirement
"Needs-input '(?)' propagates up the orchestration tree to the root (area
rail)"). The rollup is computed by `internal/tui/hera/model.go`'s
`rollupNeedsInput()` / `orchSubtreeNeedsInput()`, which walks
`BridgeSubtree(orchID)` (the same cycle-safe traversal used for rail nesting and
the Ctrl+D cascade) and OR's together every reachable role's own needs-input
signal (`RoleView.needsInputOwn()`: live PTY needs-input OR hera `blocked`
status).

Separately, pressing `a` on a rail row archives that ROLE (`Ops.ArchiveToggle` →
`db.ArchiveHeraRole`, `internal/tui/hera/ops.go:96-108`) — a reversible, Tier-1
"hide" that only stamps `archived_at`. It deliberately does NOT end the live
binding or touch the role's hera status (spec: "keeps the session + worktree
alive (no detach)"), so an archived role's own needs-input signal can still be
genuinely true afterward.

Today, `orchSubtreeNeedsInput` has no `Archived` gate at all, so archiving a
blocked worker (or a nested sub-coordinator's bridging row) does NOT stop it
from continuing to flag every ancestor coordinator up to the root. That is the
reported problem: the user archives a node specifically to dismiss it from
their attention, but the root coordinator keeps showing `?` anyway.

## Goals / Non-Goals

**Goals:**

- Once a role is archived (`a`), its own needs-input signal — and, if it is a
  bridging row into a nested sub-orchestrator, that whole hidden subtree's
  needs-input signal — SHALL NOT be counted toward any ancestor coordinator's
  rollup or a coordinator-less orchestrator header's rollup.
- Once a whole sub-orchestrator is archived (`a` on its header), the same
  exclusion applies when it is reached from a live ancestor via a bridge.
- The archived role's OWN rail row keeps showing its OWN needs-input glyph
  (dimmed, in place) exactly as today — this change only stops it from being
  counted by anything ABOVE it in the tree.

**Non-Goals:**

- Not changing `BridgeSubtree` itself, or anything that depends on it for
  rendering/nesting (dimmed-in-place rows, Ctrl+D cascade) — those must
  continue to show and act on archived rows exactly as they do today (spec:
  "An archived sub-orchestrator reached through a bridge renders dimmed in
  place ... NOT dropped").
- Not changing the needs-input summary box (area 5) or its existing
  archived-role exclusion — that is a different indicator with its own,
  already-correct exclusion.
- Not changing what makes a role "need input" in the first place (PTY scan,
  `blocked` status) — only what counts toward an ancestor's rollup.

## Decisions

**Add a dedicated, archive-aware traversal inside `orchSubtreeNeedsInput`
instead of reusing `BridgeSubtree`.**

`BridgeSubtree` is shared with rendering and the Ctrl+D cascade, both of which
must keep including archived rows (dimmed-in-place, still nukeable). It also
decides whether to descend into a bridged child purely from `roleBridges(w)`
(live-or-non-teardown), with no `Archived` check on the bridging role — so an
archived nested-sub-coordinator's bridging row does not stop `BridgeSubtree`
from walking into its child. Reusing `BridgeSubtree` and post-filtering its
flattened output would therefore only catch two of the three archived cases
(a directly-archived leaf role, and a whole child orchestrator archived via
its OWN header) but MISS the actual reported case: archiving the
bridging row that represents a nested sub-coordinator, while its child
orchestrator's own `archived_at` stays unset.

The rollup instead needs its own recursive walk that gates DESCENT on the
bridging role's `Archived` flag, in addition to skipping any archived role's
own signal:

```go
func (m *Model) orchSubtreeNeedsInput(orchID int64) bool {
	start := m.OrchByID(orchID)
	if start == nil {
		return false
	}
	bridge := m.bridgeIndex()
	visited := make(map[int64]bool)
	var walk func(o *OrchView) bool
	walk = func(o *OrchView) bool {
		if o == nil || visited[o.ID] {
			return false
		}
		visited[o.ID] = true
		for i := range o.Roles {
			w := &o.Roles[i]
			if w.Archived {
				continue // archived: excluded from ancestor rollup, own signal AND anything bridged beneath it
			}
			if w.needsInputOwn() {
				return true
			}
			if w.Kind != db.HeraKindCoordinator && roleBridges(w) {
				if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != o.ID && walk(c) {
					return true
				}
			}
		}
		for _, c := range m.coordBridgeChildren(o) {
			// coordBridgeChildren already excludes archived children (model.go:457)
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(start)
}
```

This mirrors `BridgeSubtree`'s shape (same visited-set cycle guard, same two
descent mechanisms: worker-bridge and `coordBridgeChildren`) so its behavior
stays consistent with rail nesting for everything EXCEPT the archived-pruning
rule, which is the entire point of the change.

Note the walk always fully evaluates the ROOT (`start`) regardless of whether
the root orchestrator itself is archived — archiving stops a node's signal from
propagating to its ANCESTORS, it does not blank out its own header's rollup
over its own (non-archived) children.

**The per-role `SubtreeNeedsInput` stamping in `rollupNeedsInput` (phase 2)
does not need to change.** It already computes each row's OWN displayed glyph
independently (`rv.needsInputOwn() || subtree[c.ID]`), and that computation is
intentionally left untouched: an archived bridging row keeps showing `?` on
itself when its hidden children need input (per the Goals above), while
`orchSubtreeNeedsInput`'s calls into `subtree[...]` for ANY grandparent walk
are the ones now archive-aware. Because the new traversal directly re-walks
role state each time rather than reading other roles' already-stamped
`SubtreeNeedsInput`, there is no ordering hazard between the two phases.

## Risks / Trade-offs

- **Duplicated traversal shape vs. `BridgeSubtree`** → Accepted: the two
  traversals have different pruning rules by design (rendering must keep
  archived nodes, rollup must drop them), so a shared traversal would need a
  parameterized "prune archived" flag threaded through, which is arguably
  less readable than two small, independently testable walks. If a third
  traversal with yet another pruning rule shows up later, revisit factoring a
  shared cycle-safe walker with a callback.
- **Cycle safety** → Mitigated: the new walk keeps the same `visited` map
  cycle guard as `BridgeSubtree`, so a bridge cycle still terminates.

## Acceptance criteria

- It should exclude a directly-archived leaf worker's own needs-input signal from its parent coordinator's rollup.
- It should exclude a directly-archived leaf worker's signal from propagating further, to the root coordinator, when it sits multiple bridge levels down.
- It should exclude an archived nested-sub-coordinator's bridging row from bubbling up needs-input from ANY of its (non-archived) descendants.
- It should exclude an archived whole sub-orchestrator's subtree when reached via a worker-bridge from a live ancestor.
- It should continue showing the needs-input glyph on the archived role's OWN row.
- It should continue rolling up needs-input normally when nothing in the subtree is archived (no regression of the existing BUG-018 behavior).
- It should remain cycle-safe when the orchestration subtree contains a bridge cycle and one of the cyclic members is archived.
