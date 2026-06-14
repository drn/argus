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

func TestReadHeraCoordinators_SelectsCoordinatorRole(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.SetMeta("coord", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))
	testutil.NoError(t, d.SetMeta("worker", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)))
	// A different namespace must not leak in.
	testutil.NoError(t, d.SetMeta("other", "pr", "state", "approved"))

	got := app.readHeraCoordinators()
	testutil.Equal(t, got["coord"], true)
	_, hasWorker := got["worker"]
	testutil.Equal(t, hasWorker, false)
	_, hasOther := got["other"]
	testutil.Equal(t, hasOther, false)
}

func TestReadHeraCoordinators_EmptyReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	got := app.readHeraCoordinators()
	testutil.Nil(t, got)
}

func TestReadHeraCoordinators_QueryErrorReturnsNil(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	testutil.NoError(t, d.Close()) // subsequent meta query errors
	got := app.readHeraCoordinators()
	testutil.Nil(t, got)
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
