package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/dagview"
)

// orchView is a tiny builder for a single orchestrator: the first (coord, task)
// pair is the coordinator, the rest are live workers.
func orchView(id int64, name, coordTask string, workers ...struct{ name, task string }) OrchView {
	o := OrchView{ID: id, Name: name}
	if coordTask != "" {
		o.Roles = append(o.Roles, RoleView{
			Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: coordTask,
			TaskStatus: "in_progress",
		})
	}
	for _, w := range workers {
		o.Roles = append(o.Roles, RoleView{
			Name: w.name, Kind: db.HeraKindWorker, Live: true, TaskID: w.task,
			TaskStatus: "in_progress",
		})
	}
	return o
}

func wk(name, task string) struct{ name, task string } {
	return struct{ name, task string }{name, task}
}

// depsByID returns the resolved DependsOn slice for the node with id, or nil.
func depsByID(nodes []dagview.Node, id string) []string {
	for _, n := range nodes {
		if n.ID == id {
			return n.DependsOn
		}
	}
	return nil
}

func nodeIDs(nodes []dagview.Node) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out
}

func TestHeraTreeNodes(t *testing.T) {
	t.Run("nil root yields nil", func(t *testing.T) {
		testutil.Nil(t, heraTreeNodes(Model{}, nil))
	})

	t.Run("coordinator is root; workers hang off it", func(t *testing.T) {
		root := orchView(1, "orch", "tc", wk("w1", "ta"), wk("w2", "tb"))
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, len(nodes), 3)
		testutil.DeepEqual(t, depsByID(nodes, "tc"), nil)            // root, no parent
		testutil.DeepEqual(t, depsByID(nodes, "ta"), []string{"tc"}) // worker → coord
		testutil.DeepEqual(t, depsByID(nodes, "tb"), []string{"tc"})
	})

	t.Run("multi-binding sub-coordinator bridge stitches subtrees", func(t *testing.T) {
		// A: coord=ta, worker=tb. B: coord=tb (same task tb), worker=tc.
		// tb is bound BOTH as a worker under A and the coordinator under B, so it
		// must collapse to ONE node carrying a parent edge (→ta) AND parenting tc.
		a := orchView(1, "A", "ta", wk("w", "tb"))
		b := orchView(2, "B", "tb", wk("w", "tc"))
		m := Model{Active: []OrchView{a, b}}
		nodes := heraTreeNodes(m, &m.Active[0]) // root = A

		ids := nodeIDs(nodes)
		testutil.Equal(t, ids["ta"] && ids["tb"] && ids["tc"], true)
		testutil.Equal(t, len(nodes), 3)                             // tb deduped to one node
		testutil.DeepEqual(t, depsByID(nodes, "ta"), nil)            // root coord
		testutil.DeepEqual(t, depsByID(nodes, "tb"), []string{"ta"}) // worker-in-A edge
		testutil.DeepEqual(t, depsByID(nodes, "tc"), []string{"tb"}) // worker-in-B edge
	})

	t.Run("scoped to subtree: a sibling orchestrator is excluded", func(t *testing.T) {
		a := orchView(1, "A", "ta", wk("w", "tb"))
		other := orchView(9, "Other", "tz", wk("w", "ty")) // not reachable from A
		m := Model{Active: []OrchView{a, other}}
		nodes := heraTreeNodes(m, &m.Active[0])
		ids := nodeIDs(nodes)
		testutil.Equal(t, ids["ta"] && ids["tb"], true)
		testutil.Equal(t, ids["tz"] || ids["ty"], false) // sibling not pulled in
	})

	t.Run("cyclic multi-binding terminates and emits both nodes", func(t *testing.T) {
		// A's worker tb is B's coordinator; B's worker ta is A's coordinator.
		// The BFS visited-set (keyed by orch ID) must terminate — a hang here
		// trips the test timeout. The resulting node graph is cyclic, but the
		// dagview layout's own cycle guard handles rendering.
		a := orchView(1, "A", "ta", wk("w", "tb"))
		b := orchView(2, "B", "tb", wk("w", "ta"))
		m := Model{Active: []OrchView{a, b}}
		nodes := heraTreeNodes(m, &m.Active[0]) // must return (not hang)
		ids := nodeIDs(nodes)
		testutil.Equal(t, ids["ta"] && ids["tb"], true)
	})

	t.Run("archived roles are skipped", func(t *testing.T) {
		root := orchView(1, "orch", "tc", wk("w1", "ta"))
		root.Roles = append(root.Roles, RoleView{
			Name: "old", Kind: db.HeraKindWorker, Live: true, TaskID: "tarch", Archived: true,
		})
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, nodeIDs(nodes)["tarch"], false)
		testutil.Equal(t, len(nodes), 2) // coord + live worker only
	})

	t.Run("degenerate coord==worker task gets no self-edge", func(t *testing.T) {
		root := OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tx"},
			{Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "tx"}, // same task
		}}
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, len(nodes), 1)                  // deduped
		testutil.DeepEqual(t, depsByID(nodes, "tx"), nil) // no self-edge
	})

	t.Run("no live coordinator: workers still render, no synthetic edge", func(t *testing.T) {
		root := OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "ta"},
			{Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tb"},
		}}
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, len(nodes), 2)
		testutil.DeepEqual(t, depsByID(nodes, "ta"), nil)
		testutil.DeepEqual(t, depsByID(nodes, "tb"), nil)
	})

	t.Run("unbound and empty-task roles are skipped", func(t *testing.T) {
		root := OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
			{Name: "dead", Kind: db.HeraKindWorker, Live: false, TaskID: "td"}, // not live
			{Name: "unbound", Kind: db.HeraKindWorker, Live: true, TaskID: ""}, // no task
		}}
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, len(nodes), 1) // only the coordinator
		testutil.Equal(t, nodes[0].ID, "tc")
	})

	t.Run("node carries the bound task's status and result", func(t *testing.T) {
		root := OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc",
				TaskStatus: "complete", TaskResult: `{"failed":true}`},
		}}
		m := Model{Active: []OrchView{root}}
		nodes := heraTreeNodes(m, &m.Active[0])
		testutil.Equal(t, len(nodes), 1)
		testutil.Equal(t, nodes[0].Status, "complete")
		testutil.Equal(t, nodes[0].Result, `{"failed":true}`)
	})
}
