package db

// Stage 1 failing tests for sub-coordinator plan nodes (add-hera-subcoord-nodes).
//
// These tests reference planned API that does not yet exist:
//   - HeraNodeKind / HeraNodeKindWorker / HeraNodeKindSubCoord constants
//   - CreateHeraRoleInput.NodeKind and .Goal fields
//   - HeraPlannedNodeSpec.NodeKind and .Goal fields
//   - HeraRole.NodeKind and .Goal fields (surfaced on planned-role rows)
//   - ListHeraPlannedNodes returning NodeKind + Goal
//
// All tests in this file fail to compile until Stage 2 adds the above.

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestSubCoord_PlannedRolePersistsSubCoordKindAndGoal covers:
//   - "Sub-coordinator node kind is accepted and persisted"
//   - CreateHeraPlannedRole with NodeKind=subcoord stores the discriminator + goal
//   - ListHeraPlannedNodes surfaces NodeKind=subcoord and the goal
func TestSubCoord_PlannedRolePersistsSubCoordKindAndGoal(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	goal := "build the authentication sub-system end-to-end"
	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orch,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         goal, // goal is the delivery prompt on the role
		NodeKind:       HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	// The role row persists the subcoord discriminator.
	testutil.Equal(t, r.NodeKind, HeraNodeKindSubCoord)
	// The role's Prompt IS the goal (no separate Goal column needed on the in-mem struct).
	testutil.Equal(t, r.Prompt, goal)
	// The hera_roles kind stays worker (D2: subcoord is a worker in the parent DAG).
	testutil.Equal(t, r.Kind, HeraKindWorker)

	// ListHeraPlannedNodes surfaces NodeKind=subcoord.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].ID, r.ID)
	testutil.Equal(t, planned[0].NodeKind, HeraNodeKindSubCoord)
	testutil.Equal(t, planned[0].Prompt, goal)
}

// TestSubCoord_AbsentNodeKindDefaultsToWorker covers:
//   - "Absent kind defaults to leaf worker"
//   - A planned role created without NodeKind is treated as HeraNodeKindWorker.
//   - ListHeraPlannedNodes returns NodeKind=worker for such rows.
func TestSubCoord_AbsentNodeKindDefaultsToWorker(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	// No NodeKind supplied — must default to worker.
	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orch,
		Name:           "1a-worker",
		ArgusProject:   "proj",
		Prompt:         "do the work",
		// NodeKind intentionally omitted
	})
	testutil.NoError(t, err)
	testutil.Equal(t, r.NodeKind, HeraNodeKindWorker)

	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].NodeKind, HeraNodeKindWorker)
}

// TestSubCoord_ExplicitWorkerNodeKind covers byte-identical behaviour when
// NodeKind is explicitly set to worker — identical to the absent-kind case.
func TestSubCoord_ExplicitWorkerNodeKind(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orch,
		Name:           "1b-worker",
		ArgusProject:   "proj",
		Prompt:         "do the work",
		NodeKind:       HeraNodeKindWorker,
	})
	testutil.NoError(t, err)
	testutil.Equal(t, r.NodeKind, HeraNodeKindWorker)
	testutil.Equal(t, r.Kind, HeraKindWorker)
}

// TestSubCoord_CreateHeraPlanWithMixedNodeKinds covers:
//   - "Whole-graph submission mixes node kinds" (store layer)
//   - CreateHeraPlan accepts nodes with different NodeKind values in one batch.
//   - Each node persists its specified NodeKind.
func TestSubCoord_CreateHeraPlanWithMixedNodeKinds(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	created, err := d.CreateHeraPlan(orch,
		[]HeraPlannedNodeSpec{
			{Name: "1a-worker", ArgusProject: "proj", Prompt: "leaf work", NodeKind: HeraNodeKindWorker},
			{Name: "2a-subcoord", ArgusProject: "proj", Prompt: "run auth sub-team", NodeKind: HeraNodeKindSubCoord},
		},
		// 2a is blocked by 1a.
		[]HeraBlockSpec{
			{BlockedNodeIdx: 1, BlockerNodeIdx: 0},
		},
	)
	testutil.NoError(t, err)
	testutil.Equal(t, len(created), 2)
	testutil.Equal(t, created[0].NodeKind, HeraNodeKindWorker)
	testutil.Equal(t, created[1].NodeKind, HeraNodeKindSubCoord)

	// Both appear in the planned-node list with correct kinds.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 2)
	byID := map[int64]*HeraRole{}
	for _, p := range planned {
		byID[p.ID] = p
	}
	testutil.Equal(t, byID[created[0].ID].NodeKind, HeraNodeKindWorker)
	testutil.Equal(t, byID[created[1].ID].NodeKind, HeraNodeKindSubCoord)
}

// TestSubCoord_SubCoordNodeHasBinding_ExcludedFromPlanned verifies that a
// subcoord node with a binding (materialized) is excluded from ListHeraPlannedNodes,
// identical to a worker node — the NodeKind discriminator does not change the
// idempotency guard.
func TestSubCoord_SubCoordNodeHasBinding_ExcludedFromPlanned(t *testing.T) {
	d := testDB(t)
	orch := planTestOrch(t, d, "orch")

	r, err := d.CreateHeraPlannedRole(CreateHeraRoleInput{
		OrchestratorID: orch,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         "build auth",
		NodeKind:       HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	// Simulate materialization: give the subcoord node a binding.
	_, err = d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: r.ID, OrchestratorID: orch, ArgusTaskID: "task-sc-1", WorktreePath: "/wt/sc",
	})
	testutil.NoError(t, err)

	// No longer a planned node — it has a binding.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 0)
}
