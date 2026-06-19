package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// planTestOrch creates an orchestrator and returns its id.
func planTestOrch(t *testing.T, d *DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name)
	testutil.NoError(t, err)
	return o.ID
}

// plannedRole creates a planned (bindingless) worker role and returns it.
func plannedRole(t *testing.T, d *DB, orchID int64, name string) *HeraRole {
	t.Helper()
	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orchID,
		Name:           name,
		ArgusProject:   "proj",
		Prompt:         "do " + name,
	})
	testutil.NoError(t, err)
	return r
}

func TestCreateHeraPlannedRole_NoBinding(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	r := plannedRole(t, d, orch, "2c-fact-checker")

	// Persisted as a worker-kind role with the short-id-prefixed name and inputs.
	testutil.Equal(t, r.Kind, HeraKindWorker)
	testutil.Equal(t, r.Name, "2c-fact-checker")
	testutil.Equal(t, r.ArgusProject, "proj")
	testutil.Equal(t, r.Prompt, "do 2c-fact-checker")

	// No binding exists.
	has, err := d.HeraRoleHasBinding(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, has, false)

	// And it shows up in the planned-node list.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].ID, r.ID)
}

func TestCreateHeraPlannedRole_ForcesWorkerKind(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orch,
		Name:           "n",
		Kind:           HeraKindCoordinator, // should be overridden
		ArgusProject:   "proj",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, r.Kind, HeraKindWorker)
}

func TestListHeraPlannedNodes_ExcludesBoundAndArchived(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	planned := plannedRole(t, d, orch, "planned")

	// A born-bound worker (has a binding) is NOT a planned node.
	bound, _, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
		OrchestratorID: orch, Name: "bound", Kind: HeraKindWorker, ArgusProject: "proj",
	}, "task-1", "/wt/bound")
	testutil.NoError(t, err)

	// An archived planned role is excluded.
	archivedPlanned := plannedRole(t, d, orch, "archived")
	testutil.NoError(t, d.ArchiveHeraRole(archivedPlanned.ID))

	got, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].ID, planned.ID)
	// guard: the bound role's id is not present
	for _, r := range got {
		if r.ID == bound.ID {
			t.Fatalf("bound role %d should not be a planned node", bound.ID)
		}
	}
}

func TestListHeraPlannedNodes_StaysPlannedAfterBindingEnds(t *testing.T) {
	// A node that was materialized (has a binding) must NOT reappear as planned
	// after its binding ends — the gater never re-materializes.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	role, binding, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
		OrchestratorID: orch, Name: "w", Kind: HeraKindWorker, ArgusProject: "proj",
	}, "task-1", "/wt/w")
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, "done"))

	got, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
	_ = role
}

func TestAddHeraBlock_AndBlockersOf(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")

	// b is blocked by a.
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))

	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{a.ID})

	// a has no blockers.
	none, err := d.HeraBlockersOf(a.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(none), 0)
}

func TestAddHeraBlock_Idempotent(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // duplicate is a no-op
	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 1)
}

func TestAddHeraBlock_RejectsSelfEdge(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	err := d.AddHeraBlock(a.ID, a.ID)
	testutil.ErrorIs(t, err, ErrHeraBlockSelf)
}

func TestAddHeraBlock_RejectsCycle(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	c := plannedRole(t, d, orch, "c")

	// b<-a, c<-b. Now a<-c would close a->b->c->a cycle.
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(c.ID, b.ID))
	err := d.AddHeraBlock(a.ID, c.ID)
	testutil.ErrorIs(t, err, ErrHeraBlockCycle)

	// The rejected edge was not stored.
	blockers, err := d.HeraBlockersOf(a.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 0)
}

func TestAddHeraBlock_RejectsDirectTwoCycle(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // b<-a
	err := d.AddHeraBlock(a.ID, b.ID)               // a<-b closes a 2-cycle
	testutil.ErrorIs(t, err, ErrHeraBlockCycle)
}

func TestAddHeraBlock_RejectsCrossOrchestrator(t *testing.T) {
	d := testDB(t)
	o1 := planTestOrch(t, d, "o1")
	o2 := planTestOrch(t, d, "o2")
	a := plannedRole(t, d, o1, "a")
	b := plannedRole(t, d, o2, "b")
	err := d.AddHeraBlock(b.ID, a.ID)
	testutil.ErrorIs(t, err, ErrHeraBlockCrossOrchestrator)
}

func TestAddHeraBlock_SameOrchParentLevelEdgeAccepted(t *testing.T) {
	// Hierarchical composition: two sub-coord worker roles in the SAME parent
	// orchestrator may be connected by a blocking edge (phase sequencing). Each
	// sub-coord owns its own child orchestrator DAG, which stays independent.
	d := testDB(t)
	parent := planTestOrch(t, d, "parent")
	subA := plannedRole(t, d, parent, "subA")
	subB := plannedRole(t, d, parent, "subB")
	testutil.NoError(t, d.AddHeraBlock(subB.ID, subA.ID))

	// Each sub-coord's child orchestrator has its own edges, unconnected.
	childA := planTestOrch(t, d, "childA")
	ca1 := plannedRole(t, d, childA, "ca1")
	ca2 := plannedRole(t, d, childA, "ca2")
	testutil.NoError(t, d.AddHeraBlock(ca2.ID, ca1.ID))

	// subB still has exactly one blocker (subA) — no child edge leaked across.
	blockers, err := d.HeraBlockersOf(subB.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{subA.ID})
}

func TestHeraBlock_MissingBlockerPrunedNotFatal(t *testing.T) {
	// When a blocker role is deleted mid-plan, the FK cascade removes its edges
	// so HeraBlockersOf treats the dependent as no longer blocked by it.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	c := plannedRole(t, d, orch, "c")
	testutil.NoError(t, d.AddHeraBlock(c.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(c.ID, b.ID))

	// Delete blocker a — its edge to c must vanish.
	testutil.NoError(t, d.DeleteHeraRole(a.ID))

	blockers, err := d.HeraBlockersOf(c.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{b.ID})
}

func TestAddHeraBlock_UnknownRole(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	err := d.AddHeraBlock(a.ID, 999999)
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

func TestHeraRoleHasBinding(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	planned := plannedRole(t, d, orch, "planned")
	has, err := d.HeraRoleHasBinding(planned.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, has, false)

	bound, _, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
		OrchestratorID: orch, Name: "bound", Kind: HeraKindWorker, ArgusProject: "proj",
	}, "task-1", "/wt/bound")
	testutil.NoError(t, err)
	has, err = d.HeraRoleHasBinding(bound.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, has, true)
	_ = model.StatusInProgress
}
