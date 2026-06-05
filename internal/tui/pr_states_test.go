package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
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
