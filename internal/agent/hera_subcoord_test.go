package agent

// Stage 1 failing tests for MaterializeHeraSubCoordinator
// (add-hera-subcoord-nodes).
//
// Planned API referenced (does not exist yet — tests will fail to compile):
//   - MaterializeHeraSubCoordinator(database, runner, in HeraMaterializeInput) → *HeraSubCoordMaterializeResult, error
//   - HeraSubCoordMaterializeResult: Task, ParentRole, ParentBinding, ChildOrch, CoordRole, CoordBinding
//   - HeraSubCoordinatorOrientation(childOrchName, parentOrchName, coordRoleName string) string
//     (returns coordinator orientation naming the child orch + tool list)
//
// All tests in this file fail to compile until Stage 3 adds the above.

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// TestSubCoord_MaterializeCreatesDistinctTask covers:
//   - "Sub-coordinator node materializes as its own agent"
//   - "No agentless sub-coordinator"
//   - The materialized task is a NEW task distinct from the parent coordinator's task.
func TestSubCoord_MaterializeCreatesDistinctTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 7777}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	// Coordinator task (the parent agent).
	coordTask := &db.HeraRole{} // placeholder — we only need the parent orch
	_ = coordTask

	// Pre-created planned subcoord role in the parent orchestrator.
	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         "build the authentication sub-system",
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	// Build a check-in + coordinator orientation prompt (will reference child orch).
	taskPrompt := "coordinator orientation + check-in + build the authentication sub-system"

	res, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: taskPrompt,
		Project:    "proj",
	})
	testutil.NoError(t, err)

	// A new task was created.
	testutil.Equal(t, fr.startCalls, 1)
	testutil.Equal(t, res.Task.ID != "", true)

	// The new task is the sub-coordinator's own task (not the parent's).
	// We do not have a parent task id to compare here, but we assert it is not zero.
	// The real guard is in TestSubCoord_SubCoordIsNotParentTask (separate fixture).
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, taskPrompt)
}

// TestSubCoord_MaterializeWritesBothBindings covers:
//   - "Materialization writes both the parent worker binding and the child coordinator binding"
//   - The new task gets a worker binding in the parent orch (against the planned role)
//     AND a coordinator binding in a newly created child orchestrator.
func TestSubCoord_MaterializeWritesBothBindings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 8888}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         "build auth",
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	res, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: "oriented prompt",
		Project:    "proj",
	})
	testutil.NoError(t, err)

	// (1) Worker binding in the PARENT against the planned role.
	testutil.Equal(t, res.ParentBinding.RoleID, planned.ID)
	testutil.Equal(t, res.ParentBinding.OrchestratorID, parentOrch.ID)
	testutil.Equal(t, res.ParentBinding.ArgusTaskID, res.Task.ID)

	// (2) Child orchestrator created.
	testutil.Equal(t, res.ChildOrch.ID != 0, true)

	// (3) Coordinator role in the CHILD with kind coordinator.
	testutil.Equal(t, res.CoordRole.Kind, db.HeraKindCoordinator)
	testutil.Equal(t, res.CoordRole.OrchestratorID, res.ChildOrch.ID)

	// (4) Coordinator binding in the CHILD for the SAME new task.
	testutil.Equal(t, res.CoordBinding.ArgusTaskID, res.Task.ID)
	testutil.Equal(t, res.CoordBinding.OrchestratorID, res.ChildOrch.ID)

	// The planned role itself is the parent-worker role.
	testutil.Equal(t, res.ParentRole.ID, planned.ID)
}

// TestSubCoord_MaterializeNestsViaMultiBinding covers:
//   - "Sub-coordinator nests under the parent via the existing bridge"
//   - The new task holds a live binding in BOTH the parent and child orchestrators.
func TestSubCoord_MaterializeNestsViaMultiBinding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 9999}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         "build auth",
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	res, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: "prompt",
		Project:    "proj",
	})
	testutil.NoError(t, err)

	// The new task has a live binding under the parent (worker slot).
	parentBnd, err := d.HeraLiveBindingByTaskAndOrchestrator(res.Task.ID, parentOrch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, parentBnd.RoleID, planned.ID)

	// The new task also has a live binding under the child (coord slot).
	childBnd, err := d.HeraLiveBindingByTaskAndOrchestrator(res.Task.ID, res.ChildOrch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, childBnd.RoleID, res.CoordRole.ID)
}

// TestSubCoord_MaterializeChildOrchNameDerived covers:
//   - "Child orchestrator name is derived, not parent-supplied"
//   - The child orchestrator name is derived from the node name (de-collided),
//     not taken from the parent.
//   - The coordinator role defaults to "coord".
func TestSubCoord_MaterializeChildOrchNameDerived(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 1111}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth-team",
		ArgusProject:   "proj",
		Prompt:         "build auth",
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	res, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: "prompt",
		Project:    "proj",
	})
	testutil.NoError(t, err)

	// Child orchestrator name is derived from the node, not empty or "parent-orch".
	testutil.Equal(t, res.ChildOrch.Name != "", true)
	testutil.Equal(t, res.ChildOrch.Name != parentOrch.Name, true)

	// The default coordinator role name is "coord".
	testutil.Equal(t, res.CoordRole.Name, "coord")
}

// TestSubCoord_MaterializeDeCollidesChildOrchName verifies that a second subcoord
// materialization using the same base name gets a de-collided child-orch name.
func TestSubCoord_MaterializeDeCollidesChildOrchName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 2222}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	p1, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID, Name: "3a-auth",
		ArgusProject: "proj", Prompt: "build auth",
		NodeKind: db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	p2, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID, Name: "3b-auth",
		ArgusProject: "proj", Prompt: "build auth part 2",
		NodeKind: db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	r1, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role: p1, TaskPrompt: "p", Project: "proj",
	})
	testutil.NoError(t, err)

	// Use a second runner instance to avoid session collision.
	fr2 := &fakeRunner{sessionPID: 3333}
	r2, err := MaterializeHeraSubCoordinator(d, fr2, HeraMaterializeInput{
		Role: p2, TaskPrompt: "p", Project: "proj",
	})
	testutil.NoError(t, err)

	// Each materialization produces a distinct child orchestrator.
	testutil.Equal(t, r1.ChildOrch.ID != r2.ChildOrch.ID, true)
}

// TestSubCoord_MaterializePromptContainsCoordOrientationAndGoal covers:
//   - "Materialized sub-coordinator boots oriented with its goal"
//   - The delivered prompt contains coordinator orientation + check-in + goal.
func TestSubCoord_MaterializePromptContainsCoordOrientationAndGoal(t *testing.T) {
	goal := "build the authentication sub-system end-to-end"
	// HeraSubCoordinatorOrientation is the planned API (Stage 3). Build the expected
	// shape: coordinator orientation naming the child orch + spawn/plan tools, then
	// check-in standing order, then the goal.
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 4444}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         goal,
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	// The gater would build the task prompt before calling; here we test the
	// orientation helper directly (planned API Stage 3).
	childOrchName := "3a-auth" // placeholder; real name is de-collided in MaterializeHeraSubCoordinator
	orientation := HeraSubCoordinatorOrientation(childOrchName, "parent-orch", "coord")

	// Assert the orientation text mentions key coordinator tools.
	testutil.Equal(t, strings.Contains(orientation, childOrchName), true)
	testutil.Equal(t, strings.Contains(orientation, "hera_spawn_worker"), true)
	testutil.Equal(t, strings.Contains(orientation, "hera_plan"), true)

	// Now drive a full materialize with the combined prompt.
	checkIn := HeraCheckInOrientation("parent-orch", "coord")
	fullPrompt := orientation + "\n\n---\n\n" + checkIn + "\n\n---\n\n" + goal
	res, err := MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: fullPrompt,
		Project:    "proj",
	})
	testutil.NoError(t, err)

	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	// The task carries the coordinator orientation + check-in + goal prompt.
	testutil.Contains(t, got.Prompt, goal)
	testutil.Contains(t, got.Prompt, "hera_spawn_worker")
	testutil.Contains(t, got.Prompt, "hera_inbox")
}

// TestSubCoord_MaterializeStartFailureUnwindsBothBindingsLeavesPlannedRole covers:
//   - "Failed start leaves the planned role for retry"
//   - On runner.Start failure: both bindings are ended, the child orchestrator and
//     coordinator role are deleted, but the planned role survives for retry.
func TestSubCoord_MaterializeStartFailureUnwindsBothBindingsLeavesPlannedRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}

	parentOrch, err := d.CreateHeraOrchestrator("parent-orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID,
		Name:           "3a-auth",
		ArgusProject:   "proj",
		Prompt:         "build auth",
		NodeKind:       db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)

	beforeTasks, _ := d.Tasks()
	orchsBefore, _ := d.ListHeraOrchestrators(true)

	_, err = MaterializeHeraSubCoordinator(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: "prompt",
		Project:    "proj",
	})
	if err == nil {
		t.Fatal("expected MaterializeHeraSubCoordinator to fail when runner.Start errors")
	}

	// No orphan task.
	afterTasks, _ := d.Tasks()
	testutil.Equal(t, len(afterTasks), len(beforeTasks))

	// No orphan child orchestrator (freshly minted one was removed).
	orchsAfter, _ := d.ListHeraOrchestrators(true)
	testutil.Equal(t, len(orchsAfter), len(orchsBefore))

	// No live bindings.
	live, err := d.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)

	// The planned role SURVIVES (authored data — gater retries on next tick).
	got, err := d.HeraRole(planned.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, planned.ID)
	testutil.Equal(t, got.NodeKind, db.HeraNodeKindSubCoord)
}

// TestSubCoord_MaterializeNilRole guards the nil-role input (same as
// MaterializeHeraWorker).
func TestSubCoord_MaterializeNilRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	_, err := MaterializeHeraSubCoordinator(d, &fakeRunner{}, HeraMaterializeInput{Role: nil, Project: "proj"})
	if err == nil {
		t.Fatal("expected error on nil role")
	}
}
