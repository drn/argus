package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestSmoke_TaskStatusAdvanceCompletesWithoutConfirmation(t *testing.T) {
	d := testDB(t)
	task := &model.Task{ID: "target", Name: "target", Project: "p", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(task))
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { app.tasklist.SelectByID(task.ID) })

	testutil.NoError(t, sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone)))
	syncUI(t, app.tapp)
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
}
