package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// planTestOrch creates an orchestrator and returns its id.
func planTestOrch(t *testing.T, d *DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name, "")
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

func TestAddHeraBlock_RejectsCoordinatorBlocker(t *testing.T) {
	// BUG-003: a coordinator role never reaches role-status done (its session is
	// alive for the whole orchestration), so an edge whose BLOCKER is a coordinator
	// is a permanently-unsatisfiable dependency. Reject it at creation with a clear
	// error rather than silently planning the dependent forever.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	coord, _, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
		OrchestratorID: orch, Name: "coord", Kind: HeraKindCoordinator, ArgusProject: "proj",
	}, "coord-task", "/wt/coord")
	testutil.NoError(t, err)
	node := plannedRole(t, d, orch, "4a-flex")

	err = d.AddHeraBlock(node.ID, coord.ID)
	testutil.ErrorIs(t, err, ErrHeraBlockCoordinator)

	// No edge was written — the dependent has no blockers.
	blockers, err := d.HeraBlockersOf(node.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 0)
}

func TestAddHeraBlock_AcceptsCoordinatorAsBlocked(t *testing.T) {
	// The rejection is BLOCKER-side only: a coordinator may be the BLOCKED endpoint
	// (it can wait on a worker, even if that is an unusual plan shape). Only a
	// coordinator-as-blocker is the never-satisfiable case.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	coord, _, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
		OrchestratorID: orch, Name: "coord", Kind: HeraKindCoordinator, ArgusProject: "proj",
	}, "coord-task", "/wt/coord")
	testutil.NoError(t, err)
	worker := plannedRole(t, d, orch, "1a")

	testutil.NoError(t, d.AddHeraBlock(coord.ID, worker.ID))
}

func TestAddHeraBlock_UnknownRole(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	err := d.AddHeraBlock(a.ID, 999999)
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

// --- ListHeraBlocks (add-hera-plan-view, data-persistence delta) ---

// TestListHeraBlocks_ReturnsAllEdgesDeterministic mirrors the
// data-persistence scenario "Returns all edges for an orchestrator": every
// hera_blocks edge in the orchestrator, ordered by blocked then blocker role
// id.
func TestListHeraBlocks_ReturnsAllEdgesDeterministic(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r1a := plannedRole(t, d, orch, "1a")
	r2a := plannedRole(t, d, orch, "2a")
	r2b := plannedRole(t, d, orch, "2b")
	r3a := plannedRole(t, d, orch, "3a")

	// Edges: 3a←2b, 2a←1a (insert out of order to prove the query sorts them).
	testutil.NoError(t, d.AddHeraBlock(r3a.ID, r2b.ID))
	testutil.NoError(t, d.AddHeraBlock(r2a.ID, r1a.ID))

	got, err := d.ListHeraBlocks(orch)
	testutil.NoError(t, err)
	// Deterministic order: by blocked role id then blocker role id. r2a < r3a, so
	// the 2a←1a edge sorts before the 3a←2b edge.
	testutil.DeepEqual(t, got, []HeraBlock{
		{BlockedRoleID: r2a.ID, BlockerRoleID: r1a.ID},
		{BlockedRoleID: r3a.ID, BlockerRoleID: r2b.ID},
	})
}

// TestListHeraBlocks_EmptyWhenNoPlan mirrors "Empty when no plan authored": an
// orchestrator with no edges returns an empty slice without error.
func TestListHeraBlocks_EmptyWhenNoPlan(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	plannedRole(t, d, orch, "lonely") // a role but no edges

	got, err := d.ListHeraBlocks(orch)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

// TestListHeraBlocks_ScopedToOrchestrator: edges from a DIFFERENT orchestrator
// never leak into the result.
func TestListHeraBlocks_ScopedToOrchestrator(t *testing.T) {
	d := testDB(t)
	o1 := planTestOrch(t, d, "o1")
	o2 := planTestOrch(t, d, "o2")
	a1 := plannedRole(t, d, o1, "a")
	b1 := plannedRole(t, d, o1, "b")
	a2 := plannedRole(t, d, o2, "a")
	b2 := plannedRole(t, d, o2, "b")
	testutil.NoError(t, d.AddHeraBlock(b1.ID, a1.ID))
	testutil.NoError(t, d.AddHeraBlock(b2.ID, a2.ID))

	got, err := d.ListHeraBlocks(o1)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []HeraBlock{{BlockedRoleID: b1.ID, BlockerRoleID: a1.ID}})
}

// TestListHeraBlocks_ExcludesArchivedEndpoints: an edge whose blocked OR blocker
// role is archived is excluded, matching how the view filters roles.
func TestListHeraBlocks_ExcludesArchivedEndpoints(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	c := plannedRole(t, d, orch, "c")
	dd := plannedRole(t, d, orch, "d")
	// b←a (both live), and d←c. Archive c so the d←c edge drops.
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(dd.ID, c.ID))
	testutil.NoError(t, d.ArchiveHeraRole(c.ID))

	got, err := d.ListHeraBlocks(orch)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []HeraBlock{{BlockedRoleID: b.ID, BlockerRoleID: a.ID}})
}

// TestListHeraBlocks_ExcludesNukedEndpoints: an edge whose endpoint is nuked
// (Tier-2 EOL) is excluded just like an archived endpoint.
func TestListHeraBlocks_ExcludesNukedEndpoints(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	c := plannedRole(t, d, orch, "c")
	dd := plannedRole(t, d, orch, "d")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(dd.ID, c.ID))
	testutil.NoError(t, d.NukeHeraRole(c.ID))

	got, err := d.ListHeraBlocks(orch)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []HeraBlock{{BlockedRoleID: b.ID, BlockerRoleID: a.ID}})
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

// --- RemoveHeraBlock ---

func TestRemoveHeraBlock_RemovesEdge(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))

	// Verify edge exists.
	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 1)

	// Remove it.
	testutil.NoError(t, d.RemoveHeraBlock(b.ID, a.ID))

	// Edge is gone.
	after, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(after), 0)
}

func TestRemoveHeraBlock_IdempotentOnMissingEdge(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")

	// No edge was ever added — removing it must be a no-op (no error).
	testutil.NoError(t, d.RemoveHeraBlock(b.ID, a.ID))
}

func TestRemoveHeraBlock_OnlyRemovesTargetEdge(t *testing.T) {
	// When a node has two blockers, removing one must leave the other intact.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	a := plannedRole(t, d, orch, "a")
	b := plannedRole(t, d, orch, "b")
	c := plannedRole(t, d, orch, "c")
	testutil.NoError(t, d.AddHeraBlock(c.ID, a.ID))
	testutil.NoError(t, d.AddHeraBlock(c.ID, b.ID))

	testutil.NoError(t, d.RemoveHeraBlock(c.ID, a.ID))

	blockers, err := d.HeraBlockersOf(c.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{b.ID})
}

// --- UpdateHeraPlannedNode ---

func TestUpdateHeraPlannedNode_UpdatesPromptAndProject(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r := plannedRole(t, d, orch, "w")

	testutil.NoError(t, d.UpdateHeraPlannedNode(r.ID, "new prompt", "new-proj"))

	got, err := d.HeraRole(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, "new prompt")
	testutil.Equal(t, got.ArgusProject, "new-proj")
}

func TestUpdateHeraPlannedNode_PreservesProjectOnEmpty(t *testing.T) {
	// An empty project string must leave the existing project unchanged.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r := plannedRole(t, d, orch, "w") // argus_project="proj" from plannedRole helper

	testutil.NoError(t, d.UpdateHeraPlannedNode(r.ID, "new prompt", ""))

	got, err := d.HeraRole(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, "new prompt")
	testutil.Equal(t, got.ArgusProject, "proj") // unchanged
}

// --- CancelHeraPlannedNode ---

func TestCancelHeraPlannedNode_StampsCancelledAt(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r := plannedRole(t, d, orch, "w")
	testutil.Nil(t, r.CancelledAt)

	testutil.NoError(t, d.CancelHeraPlannedNode(r.ID))

	got, err := d.HeraRole(r.ID)
	testutil.NoError(t, err)
	if got.CancelledAt == nil {
		t.Fatal("expected CancelledAt to be set after cancel")
	}
}

func TestCancelHeraPlannedNode_Idempotent(t *testing.T) {
	// A second cancel must preserve the original timestamp, not clobber it.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	r := plannedRole(t, d, orch, "w")

	testutil.NoError(t, d.CancelHeraPlannedNode(r.ID))
	first, err := d.HeraRole(r.ID)
	testutil.NoError(t, err)
	if first.CancelledAt == nil {
		t.Fatal("expected CancelledAt after first cancel")
	}

	testutil.NoError(t, d.CancelHeraPlannedNode(r.ID))
	second, err := d.HeraRole(r.ID)
	testutil.NoError(t, err)
	if second.CancelledAt == nil {
		t.Fatal("expected CancelledAt after second cancel")
	}

	// COALESCE preserves the first timestamp — both reads return the same value.
	if !first.CancelledAt.Equal(*second.CancelledAt) {
		t.Fatalf("idempotent cancel changed timestamp: first=%v second=%v", first.CancelledAt, second.CancelledAt)
	}
}

func TestCancelHeraPlannedNode_ExcludedFromListHeraPlannedNodes(t *testing.T) {
	// A cancelled node must NOT appear in ListHeraPlannedNodes — the gater must
	// never attempt to materialize it.
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	active := plannedRole(t, d, orch, "active")
	toCancel := plannedRole(t, d, orch, "cancelled")

	testutil.NoError(t, d.CancelHeraPlannedNode(toCancel.ID))

	nodes, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(nodes), 1)
	testutil.Equal(t, nodes[0].ID, active.ID)
}

func TestCancelHeraPlannedNode_StillVisibleViaByID(t *testing.T) {
	// Cancelled nodes are kept in the DB (not deleted): HeraRole(id) and
	// ListHeraBlocks still surface them (for the plan view).
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")
	dep := plannedRole(t, d, orch, "dep")
	blocker := plannedRole(t, d, orch, "blocker")
	testutil.NoError(t, d.AddHeraBlock(dep.ID, blocker.ID))
	testutil.NoError(t, d.CancelHeraPlannedNode(blocker.ID))

	// By-id lookup still works.
	got, err := d.HeraRole(blocker.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, blocker.ID)
	if got.CancelledAt == nil {
		t.Fatal("expected CancelledAt to be set")
	}

	// ListHeraBlocks still surfaces the edge (plan view needs it to render the
	// cancelled node in its position in the graph).
	blocks, err := d.ListHeraBlocks(orch)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blocks), 1)
	testutil.Equal(t, blocks[0].BlockerRoleID, blocker.ID)
}
