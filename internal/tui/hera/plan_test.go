package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/planview"
)

// seedPlannedRole creates a planned (never-bound) worker role and returns it.
func seedPlannedRole(t *testing.T, d *db.DB, orchID int64, name string) *db.HeraRole {
	t.Helper()
	r, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, ArgusProject: "p", Prompt: "do " + name,
	})
	testutil.NoError(t, err)
	return r
}

// orchViewByName builds the model and returns the OrchView with the given name
// (across all sections), or nil. BuildModel populates OrchView.Blocks and the
// RoleView.Planned discriminator (Stage 2).
func orchViewByName(t *testing.T, d *db.DB, name string) *OrchView {
	t.Helper()
	m, err := BuildModel(d, nil)
	testutil.NoError(t, err)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if sec[i].Name == name {
				return &sec[i]
			}
		}
	}
	return nil
}

// findNode returns the planview.Node with the given ID, or false.
func findNode(nodes []planview.Node, id string) (planview.Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return planview.Node{}, false
}

// --- RoleView.Planned discriminator (hera-view delta) ---

// TestRoleViewPlanned_NeverBoundIsPlanned mirrors "it should mark a never-bound
// worker role as planned and a bound (live or ended) role as not planned".
func TestRoleViewPlanned_NeverBoundIsPlanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	planned := seedPlannedRole(t, d, orch, "2a-planned")
	seedBoundRole(t, d, orch, "live", db.HeraKindWorker, "t-live")

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)

	byID := map[int64]RoleView{}
	for _, r := range ov.Roles {
		byID[r.RoleID] = r
	}
	// The planned (never-bound) worker is Planned.
	testutil.Equal(t, byID[planned.ID].Planned, true)
	// The coordinator and the live worker are NOT planned.
	for _, r := range ov.Roles {
		if r.Kind == db.HeraKindCoordinator || r.TaskID == "t-live" {
			testutil.Equal(t, r.Planned, false)
		}
	}
}

// TestRoleViewPlanned_EndedBindingIsNotPlanned: a worker that was materialized
// and whose binding then ended is NOT planned (the gater never re-materializes).
func TestRoleViewPlanned_EndedBindingIsNotPlanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	role, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch, Name: "ended", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "t-ended", "/wt/ended")
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, "done"))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	for _, r := range ov.Roles {
		if r.RoleID == role.ID {
			testutil.Equal(t, r.Planned, false)
		}
	}
}

// --- OrchView.Blocks population (hera-view delta D8) ---

// TestBuildModel_PopulatesOrchBlocks: BuildModel attaches the orchestrator's
// blocking edges to OrchView.Blocks (one bulk read).
func TestBuildModel_PopulatesOrchBlocks(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	a := seedPlannedRole(t, d, orch, "1a")
	b := seedPlannedRole(t, d, orch, "2a")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	testutil.DeepEqual(t, ov.Blocks, []db.HeraBlock{{BlockedRoleID: b.ID, BlockerRoleID: a.ID}})
}

// --- heraPlanNodes projection (hera-view delta) ---

// TestHeraPlanNodes_PlannedAndLiveWithEdges mirrors "it should project planned
// (never-bound) roles and live roles together as plan nodes with their blocking
// edges". Planned nodes carry State=StatePlanned; the blocking edge becomes a
// planview.Edge.
func TestHeraPlanNodes_PlannedAndLiveWithEdges(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	a := seedPlannedRole(t, d, orch, "1a-research")
	b := seedPlannedRole(t, d, orch, "2a-write")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)

	nodes, edges := heraPlanNodes(ov)

	// Both planned worker roles appear as nodes.
	testutil.Equal(t, len(nodes) >= 2, true)
	var seen1a, seen2a bool
	for _, n := range nodes {
		if n.Name == "1a-research" {
			seen1a = true
			testutil.Equal(t, n.Planned, true)
			testutil.Equal(t, n.State, planview.StatePlanned)
		}
		if n.Name == "2a-write" {
			seen2a = true
			testutil.Equal(t, n.Planned, true)
		}
	}
	testutil.Equal(t, seen1a, true)
	testutil.Equal(t, seen2a, true)

	// Exactly one dependency edge: 2a depends on 1a (To=2a, From=1a). Pin the
	// direction — From is the blocker (1a, upstream), To is the blocked (2a,
	// downstream) — so the stage layering matches dagview's convention.
	testutil.Equal(t, len(edges), 1)
	from2a, _ := findNode(nodes, edges[0].From)
	to2a, _ := findNode(nodes, edges[0].To)
	testutil.Equal(t, from2a.Name, "1a-research")
	testutil.Equal(t, to2a.Name, "2a-write")
}

// TestHeraPlanNodes_FailedResultIsStateFailed pins that a live node whose bound
// task reported a {"failed":true} result projects StateFailed (red ✕), winning
// over the workflow status (D7).
func TestHeraPlanNodes_FailedResultIsStateFailed(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "boom", db.HeraKindWorker, "t-boom")
	testutil.NoError(t, d.SetStatus("t-boom", model.StatusInReview))
	testutil.NoError(t, d.SetResult("t-boom", `{"failed":true}`))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, _ := heraPlanNodes(ov)
	n, ok := findNode(nodes, "t-boom")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, n.State, planview.StateFailed)
}

// TestHeraPlanNodes_LiveNodeColoursFromTaskStatus mirrors "a live node by its
// bound task status (including red ✕ on a failed result)". The projection
// stamps State from the bound task's status/result.
func TestHeraPlanNodes_LiveNodeColoursFromTaskStatus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	// A live worker whose task is complete.
	seedBoundRole(t, d, orch, "done-wkr", db.HeraKindWorker, "t-done")
	testutil.NoError(t, d.SetStatus("t-done", model.StatusComplete))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, _ := heraPlanNodes(ov)

	n, ok := findNode(nodes, "t-done")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, n.Planned, false)
	testutil.Equal(t, n.State, planview.StateDone)
}

// TestHeraPlanNodes_DegenerateNoPlanFlatStage mirrors "render the orchestrator's
// live roles as a flat edgeless stage with a 'no plan' hint when no plan is
// authored". With no planned nodes and no edges, the live workers project as
// nodes with no edges between them.
func TestHeraPlanNodes_DegenerateNoPlanFlatStage(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w1", db.HeraKindWorker, "t-w1")
	seedBoundRole(t, d, orch, "w2", db.HeraKindWorker, "t-w2")

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, edges := heraPlanNodes(ov)

	// Live workers are present as nodes; no plan = no edges.
	_, ok1 := findNode(nodes, "t-w1")
	_, ok2 := findNode(nodes, "t-w2")
	testutil.Equal(t, ok1, true)
	testutil.Equal(t, ok2, true)
	testutil.Equal(t, len(edges), 0)
}

// TestHeraPlanNodes_NilOrchEmpty: a nil orchestrator yields no nodes/edges
// without panic (remote-mode / no-selection degradation).
func TestHeraPlanNodes_NilOrchEmpty(t *testing.T) {
	nodes, edges := heraPlanNodes(nil)
	testutil.Equal(t, len(nodes), 0)
	testutil.Equal(t, len(edges), 0)
}
