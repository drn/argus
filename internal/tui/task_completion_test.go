package tui

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

type completeWriteErrorStore struct{ *db.DB }

func (s completeWriteErrorStore) SetStatus(string, model.Status) error {
	return errors.New("write failed")
}

func TestSmoke_TaskCompletionNeedsSeparateConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision rune
		want     model.Status
	}{
		{"confirm", 'y', model.StatusComplete},
		{"cancel", 'n', model.StatusInReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDB(t)
			task := &model.Task{ID: "target", Name: "target", Project: "p", Status: model.StatusInProgress}
			testutil.NoError(t, d.Add(task))
			app := New(d, agent.NewRunner(nil), false)
			sim, stop := wireApp(t, app)
			defer stop()
			readUI(t, app.tapp, func() { app.tasklist.SelectByID(task.ID) })

			for range 3 {
				sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
				syncUI(t, app.tapp)
			}
			got, err := d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Status, model.StatusInReview)
			readUI(t, app.tapp, func() {
				testutil.Equal(t, app.mode, modeConfirmComplete)
				testutil.Equal(t, app.confirmCompleteTaskID, task.ID)
			})

			sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, tc.decision, tcell.ModNone))
			syncUI(t, app.tapp)
			got, err = d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Status, tc.want)
			readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
		})
	}
}

func TestSmoke_TaskCompletionKeepsOriginalSelection(t *testing.T) {
	d := testDB(t)
	for _, id := range []string{"target", "other"} {
		testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Project: "p", Status: model.StatusInReview}))
	}
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { app.tasklist.SelectByID("target") })
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeConfirmComplete)
		app.tasklist.SelectByID("other")
	})
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	syncUI(t, app.tapp)
	target, err := d.Get("target")
	testutil.NoError(t, err)
	testutil.Equal(t, target.Status, model.StatusComplete)
	other, err := d.Get("other")
	testutil.NoError(t, err)
	testutil.Equal(t, other.Status, model.StatusInReview)
}

func TestSmoke_TaskCompletionSkipsChangedStatus(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "target", Name: "target", Project: "p", Status: model.StatusInReview}))
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { app.tasklist.SelectByID("target") })
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	syncUI(t, app.tapp)
	testutil.NoError(t, d.SetStatus("target", model.StatusInProgress))
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	syncUI(t, app.tapp)
	got, err := d.Get("target")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInProgress)
}

func TestSmoke_TaskCompletionHandlesDeletedTask(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "target", Name: "target", Project: "p", Status: model.StatusInReview}))
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { app.tasklist.SelectByID("target") })
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	syncUI(t, app.tapp)
	testutil.NoError(t, d.Delete("target"))
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Contains(t, app.statusbar.Error(), "Could not load task")
	})
}

func TestSmoke_TaskCompletionHandlesWriteFailure(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "target", Name: "target", Project: "p", Status: model.StatusInReview}))
	app := New(completeWriteErrorStore{d}, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { app.tasklist.SelectByID("target") })
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	syncUI(t, app.tapp)
	sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	syncUI(t, app.tapp)
	got, err := d.Get("target")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Contains(t, app.statusbar.Error(), "Could not mark task complete")
	})
}
