package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestReadPRStates_ParsesCachedMeta(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.SetMetaBatch("t1", "pr", map[string]string{"state": "approved", "url": "u1"}))
	testutil.NoError(t, d.SetMetaBatch("t2", "pr", map[string]string{"state": "changes-requested"}))
	// Unparseable state is skipped, not fatal.
	testutil.NoError(t, d.SetMetaBatch("t3", "pr", map[string]string{"state": "bogus"}))
	// A different namespace must not leak in.
	testutil.NoError(t, d.SetMetaBatch("t4", "other", map[string]string{"state": "approved"}))

	got := app.readPRStates()
	testutil.Equal(t, got["t1"], model.PRApproved)
	testutil.Equal(t, got["t2"], model.PRChangesRequested)
	_, hasBogus := got["t3"]
	testutil.Equal(t, hasBogus, false)
	_, hasOther := got["t4"]
	testutil.Equal(t, hasOther, false)
}

func TestReadPRStates_EmptyReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	got := app.readPRStates()
	testutil.Nil(t, got)
}

func TestReadPRStates_QueryErrorReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	testutil.NoError(t, d.Close()) // subsequent meta query errors
	got := app.readPRStates()
	testutil.Nil(t, got)
}

// TestRefreshTasks_WiresPRStatesIntoTaskList confirms the tick wiring: a cached
// pr meta row flows through refreshTasksWithIDs → tasklist.SetPRStates so the
// task row can render the indicator.
func TestRefreshTasks_WiresPRStatesIntoTaskList(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "task", Project: "p", Status: model.StatusInReview, Branch: "argus/t1"}))
	testutil.NoError(t, d.SetMetaBatch("t1", "pr", map[string]string{"state": "awaiting-review"}))

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	testutil.Equal(t, app.tasklist.PRStateFor("t1"), model.PRAwaitingReview)
}

func TestReadHeraRoles_PartitionsWorkersAndCoordinators(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.SetMeta("coord", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))
	testutil.NoError(t, d.SetMeta("worker", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)))
	// A different namespace must not leak in.
	testutil.NoError(t, d.SetMeta("other", "pr", "state", "approved"))

	workers, coords := app.readHeraRoles()
	// Coordinator partition holds only the coordinator.
	testutil.Equal(t, coords["coord"], true)
	_, coordHasWorker := coords["worker"]
	testutil.Equal(t, coordHasWorker, false)
	// Worker partition holds only the worker.
	testutil.Equal(t, workers["worker"], true)
	_, workerHasCoord := workers["coord"]
	testutil.Equal(t, workerHasCoord, false)
	// Other namespace leaks into neither set.
	_, wOther := workers["other"]
	_, cOther := coords["other"]
	testutil.Equal(t, wOther, false)
	testutil.Equal(t, cOther, false)
}

func TestReadHeraRoles_EmptyReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	workers, coords := app.readHeraRoles()
	testutil.Nil(t, workers)
	testutil.Nil(t, coords)
}

func TestReadHeraRoles_QueryErrorReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	testutil.NoError(t, d.Close()) // subsequent meta query errors
	workers, coords := app.readHeraRoles()
	testutil.Nil(t, workers)
	testutil.Nil(t, coords)
}

// TestRefreshTasks_WiresHeraCoordinatorsIntoTaskList confirms the tick wiring:
// a coordinator role meta row flows through refreshTasks → SetHeraCoordinators.
func TestRefreshTasks_WiresHeraCoordinatorsIntoTaskList(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "coordinator", Project: "p", Status: model.StatusInProgress, Branch: "argus/t1"}))
	testutil.NoError(t, d.SetMeta("t1", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	testutil.Equal(t, app.tasklist.IsHeraCoordinator("t1"), true)
}

// Tests for mergeManagedFromMeta — the extracted pure helper that implements
// the remote fallback branch of readManagedTasks (approach 2: helper extraction
// so the union logic can be tested without wiring a full remote App or store).

func TestMergeManagedFromMeta_WorkerAndCoordinatorBothManaged(t *testing.T) {
	// Tasks with worker or coordinator meta are managed; freelance-kind is absent
	// from these maps so it is not included.
	workers := map[string]bool{"w1": true, "w2": true}
	coordinators := map[string]bool{"c1": true}

	got := mergeManagedFromMeta(workers, coordinators)

	testutil.Equal(t, got["w1"], true)
	testutil.Equal(t, got["w2"], true)
	testutil.Equal(t, got["c1"], true)
	_, hasFreelance := got["freelance-task"]
	testutil.Equal(t, hasFreelance, false)
}

func TestMergeManagedFromMeta_FreelanceRoleNotIncluded(t *testing.T) {
	// readHeraRoles only populates the worker/coordinator maps; freelance-kind
	// tasks never appear in either map and therefore must NOT appear in the result.
	workers := map[string]bool{"w1": true}
	coordinators := map[string]bool{}

	got := mergeManagedFromMeta(workers, coordinators)

	testutil.Equal(t, got["w1"], true)
	testutil.Equal(t, len(got), 1)
}

func TestMergeManagedFromMeta_BothEmptyReturnsNil(t *testing.T) {
	// When both maps are empty the remote fallback must return nil (same as the
	// local path returning nil on an empty binding table).
	got := mergeManagedFromMeta(nil, nil)
	testutil.Nil(t, got)

	got = mergeManagedFromMeta(map[string]bool{}, map[string]bool{})
	testutil.Nil(t, got)
}

func TestMergeManagedFromMeta_OverlapDeduplicates(t *testing.T) {
	// A task ID present in both maps (e.g. a task that is both a coordinator and
	// a worker in different orchestrators) must appear exactly once.
	workers := map[string]bool{"overlap": true}
	coordinators := map[string]bool{"overlap": true, "coord-only": true}

	got := mergeManagedFromMeta(workers, coordinators)

	testutil.Equal(t, got["overlap"], true)
	testutil.Equal(t, got["coord-only"], true)
	testutil.Equal(t, len(got), 2)
}
