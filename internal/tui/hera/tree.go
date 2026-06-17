package hera

import (
	"slices"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/dagview"
)

// heraTreeNodes projects the Hera orchestration tree rooted at `root` into the
// embedded DAG widget's node set. It replaced the legacy depends_on projection:
// the graph is the ROLE HIERARCHY, not the task depends_on edges (which no
// longer exist). The coordinator of each orchestrator is the root of its level;
// every worker hangs off its orchestrator's coordinator; sub-coordinators bridge
// subtrees automatically because a sub-coordinator's task is the SAME argus task
// that is bound as a worker under its parent orchestrator — so it collapses to
// one node (keyed by task ID) that has both a parent edge (from the parent's
// worker role) and child edges (from its own orchestrator's workers).
//
// It is a pure function over the already-built Model (the rail's snapshot) — no
// DB read, no I/O — so it is safe on the tview thread and trivially testable.
// The subtree is discovered in-memory: orchestrator C is a child of P when C's
// live coordinator task is bound as a (non-coordinator) worker task under P.
func heraTreeNodes(m Model, root *OrchView) []dagview.Node {
	if root == nil {
		return nil
	}

	// Index every orchestrator in the model by ID (pinned + active + archived).
	// Archived orchestrators are kept in the index only so the root can be
	// resolved when the user selects an archived orchestrator directly; they are
	// PRUNED as BFS descendants below (see the archived-skip in the child walk).
	all := make(map[int64]*OrchView)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			all[sec[i].ID] = &sec[i]
		}
	}
	// The model snapshot the rail holds may have been rebuilt since `root` was
	// captured; resolve the canonical pointer by ID so we walk the live arrays.
	if cur := all[root.ID]; cur != nil {
		root = cur
	}

	// BFS the subtree from root. A child is reached via the multi-binding bridge:
	// its coordinator task appears in some ancestor's worker-task set. Archived
	// orchestrators are pruned as descendants — and because nesting flows THROUGH
	// them, their whole branch drops out — mirroring the DB-side SubtreeOrchIDs
	// (`child_orch.archived_at IS NULL`). The root is always included even if it
	// is itself archived (the user selected it explicitly).
	subtree := make([]*OrchView, 0, len(all))
	visited := map[int64]bool{root.ID: true}
	queue := []*OrchView{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		subtree = append(subtree, p)
		workers := workerTaskSet(p)
		for id, c := range all {
			if visited[id] || c.Archived {
				continue
			}
			// Worker bridge: c's coordinator task is a (non-teardown) worker task
			// under p. Coordinator-spawned sub-team: p and c share a coordinator
			// agent (p the earlier-id parent). Either reaches c as a descendant —
			// matching db.SubtreeOrchIDs' ANY-parent-side-binding join.
			if ct := c.CoordBridgeTaskID(); ct != "" && workers[ct] {
				visited[id] = true
				queue = append(queue, c)
				continue
			}
			if coordBridgeParentOf(p, c) {
				visited[id] = true
				queue = append(queue, c)
			}
		}
	}

	// Build nodes keyed by task ID (dedupe bridge tasks). Edges: worker → its
	// orchestrator's coordinator. Insertion order is BFS-stable (root first), so
	// a bridge task keeps the name of the worker role under its parent.
	nodes := make(map[string]int) // taskID -> index into out
	out := make([]dagview.Node, 0)
	for _, o := range subtree {
		coord := o.CoordTaskID()
		for i := range o.Roles {
			r := &o.Roles[i]
			if !r.Live || r.TaskID == "" || r.Archived {
				continue
			}
			idx, ok := nodes[r.TaskID]
			if !ok {
				idx = len(out)
				nodes[r.TaskID] = idx
				out = append(out, dagview.Node{
					ID:     r.TaskID,
					Name:   r.Name,
					Status: r.TaskStatus,
					Result: r.TaskResult,
				})
			}
			// A worker hangs off its orchestrator's coordinator. The coordinator
			// role itself never gets a self-edge (guarded by TaskID != coord).
			if r.Kind != db.HeraKindCoordinator && coord != "" && r.TaskID != coord {
				if !slices.Contains(out[idx].DependsOn, coord) {
					out[idx].DependsOn = append(out[idx].DependsOn, coord)
				}
			}
		}
	}
	return out
}

// workerTaskSet returns the set of STRUCTURAL bridge task IDs for the
// non-coordinator roles under o — each role's latest-binding task regardless of
// liveness, EXCLUDING roles whose latest binding was an operator teardown
// (reparented / user_deleted). Used to test whether a candidate
// sub-orchestrator's coordinator bridges into this orchestrator (the
// multi-binding parent→child relationship). Broadened from live-only so an
// ended-but-not-torn-down link still nests its child (the bridging-breadth rule).
func workerTaskSet(o *OrchView) map[string]bool {
	set := make(map[string]bool, len(o.Roles))
	for i := range o.Roles {
		r := &o.Roles[i]
		if r.Kind == db.HeraKindCoordinator || !roleBridges(r) {
			continue
		}
		if k := bridgeTaskID(r); k != "" {
			set[k] = true
		}
	}
	return set
}
