package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestSettings_TasksRail(t *testing.T) {
	sv := makeSettings(t)
	var changes []bool
	sv.OnTasksRailToggle = func(v bool) { changes = append(changes, v) }
	for _, key := range []tcell.Key{tcell.KeyEnter, tcell.KeyRight} {
		t.Run(tcell.NewEventKey(key, 0, 0).Name(), func(t *testing.T) {
			selectRowInCategory(t, sv, catAppearance, srTasksRail, "")
			testutil.Equal(t, sv.HandleKey(tcell.NewEventKey(key, 0, 0)), true)
			testutil.Equal(t, sv.database.Config().UI.TasksRail, key == tcell.KeyEnter)
			sv.Refresh()
			selectRowInCategory(t, sv, catAppearance, srTasksRail, "")
			sv.SetRect(0, 0, 100, 30)
			sv.Draw(drawSim(t))
		})
	}
	testutil.DeepEqual(t, changes, []bool{true, false})
	selectRowInCategory(t, sv, catAppearance, srTasksRail, "")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sv.database.Config().UI.TasksRail, false)
}

func TestSettings_TasksRailSaveFailure(t *testing.T) {
	d := testDB(t)
	sv := NewSettingsView(d)
	testutil.NoError(t, d.Close())
	sv.toggleTasksRail()
	testutil.Equal(t, sv.tasksRail, false)
	testutil.Contains(t, sv.tasksRailError, "Could not save:")
}
