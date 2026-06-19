package db

import (
	"testing"

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

func TestPlannedNode_ShortIDIsStableAcrossPlanEdits(t *testing.T) {
	// The planner-assigned short-id-prefixed name is a durable handle: it is
	// persisted verbatim and never recomputed by a plan operation (adding or
	// removing edges, deleting a sibling, etc.).
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	node := plannedRole(t, d, orch, "2c-fact-checker")
	sibling := plannedRole(t, d, orch, "1a-researcher")
	testutil.Equal(t, node.Name, "2c-fact-checker") // persisted verbatim

	// Plan edits: add an edge, then delete the sibling (which removes that edge).
	testutil.NoError(t, d.AddHeraBlock(node.ID, sibling.ID))
	testutil.NoError(t, d.DeleteHeraRole(sibling.ID))

	// The node's short-id-prefixed name is unchanged after the edits.
	got, err := d.HeraRole(node.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "2c-fact-checker")
	byName, err := d.HeraRoleByName(orch, "2c-fact-checker")
	testutil.NoError(t, err)
	testutil.Equal(t, byName.ID, node.ID)
}

func TestCreateHeraPlan_ValidBatchCreatesAllNodesAndEdges(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	created, err := d.CreateHeraPlan(orch,
		[]HeraPlannedNodeSpec{
			{Name: "1a", ArgusProject: "proj", Prompt: "do 1a"},
			{Name: "1b", ArgusProject: "proj", Prompt: "do 1b"},
			{Name: "2a", ArgusProject: "proj", Prompt: "do 2a"},
		},
		// 2a (idx 2) is blocked by 1a (idx 0) and 1b (idx 1).
		[]HeraBlockSpec{
			{BlockedNodeIdx: 2, BlockerNodeIdx: 0},
			{BlockedNodeIdx: 2, BlockerNodeIdx: 1},
		})
	testutil.NoError(t, err)
	testutil.Equal(t, len(created), 3)

	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 3)

	blockers, err := d.HeraBlockersOf(created[2].ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 2)
}

func TestCreateHeraPlan_PreExistingRoleEndpoint(t *testing.T) {
	// An edge endpoint may reference a pre-existing role (NodeIdx < 0, RoleID set).
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	existing := plannedRole(t, d, orch, "1a")

	created, err := d.CreateHeraPlan(orch,
		[]HeraPlannedNodeSpec{{Name: "2a", ArgusProject: "proj", Prompt: "do 2a"}},
		[]HeraBlockSpec{{BlockedNodeIdx: 0, BlockerNodeIdx: -1, BlockerRoleID: existing.ID}})
	testutil.NoError(t, err)
	testutil.Equal(t, len(created), 1)

	blockers, err := d.HeraBlockersOf(created[0].ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{existing.ID})
}

func TestCreateHeraPlan_CyclicBatchRollsBackEntirely(t *testing.T) {
	// A batch whose edges close a cycle must create ZERO rows — no orphan planned
	// nodes from the (valid) node inserts, no edges. Full rollback.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	created, err := d.CreateHeraPlan(orch,
		[]HeraPlannedNodeSpec{
			{Name: "a", ArgusProject: "proj", Prompt: "p"},
			{Name: "b", ArgusProject: "proj", Prompt: "p"},
		},
		// b<-a then a<-b closes a 2-cycle (caught by the tx-scoped check against
		// the edge inserted earlier in the SAME batch).
		[]HeraBlockSpec{
			{BlockedNodeIdx: 1, BlockerNodeIdx: 0},
			{BlockedNodeIdx: 0, BlockerNodeIdx: 1},
		})
	testutil.ErrorIs(t, err, ErrHeraBlockCycle)
	testutil.Nil(t, created)

	// Nothing persisted: no planned nodes, no roles by name.
	planned, lErr := d.ListHeraPlannedNodes()
	testutil.NoError(t, lErr)
	testutil.Equal(t, len(planned), 0)
	_, aErr := d.HeraRoleByName(orch, "a")
	testutil.ErrorIs(t, aErr, ErrHeraNotFound)
	_, bErr := d.HeraRoleByName(orch, "b")
	testutil.ErrorIs(t, bErr, ErrHeraNotFound)
}

func TestCreateHeraPlan_CrossOrchestratorEdgeRollsBack(t *testing.T) {
	// An edge to a role in a DIFFERENT orchestrator rolls back the whole batch.
	d := testDB(t)
	o1 := planTestOrch(t, d, "o1")
	o2 := planTestOrch(t, d, "o2")
	foreign := plannedRole(t, d, o2, "foreign")

	created, err := d.CreateHeraPlan(o1,
		[]HeraPlannedNodeSpec{{Name: "a", ArgusProject: "proj", Prompt: "p"}},
		[]HeraBlockSpec{{BlockedNodeIdx: 0, BlockerNodeIdx: -1, BlockerRoleID: foreign.ID}})
	testutil.ErrorIs(t, err, ErrHeraBlockCrossOrchestrator)
	testutil.Nil(t, created)

	// The valid node "a" was rolled back too.
	_, aErr := d.HeraRoleByName(o1, "a")
	testutil.ErrorIs(t, aErr, ErrHeraNotFound)
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
}
