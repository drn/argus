package taskview

import (
	"fmt"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

func TestTasksExpansionFilterInputAndHeaderStatus(t *testing.T) {
	tl := NewTaskListView()
	tl.SetRailMode(true)
	tl.SetTasks([]*model.Task{{ID: "plain", Project: "A", Name: "plain", Status: model.StatusPending}, {ID: "worker", Project: "A", Name: "worker", Status: model.StatusInProgress}})
	tl.SetManagedTasks(map[string]bool{"worker": true})
	tl.SetNeedsInput([]string{"worker"})
	sim := tcell.NewSimulationScreen("")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(80, 20)
	tl.drawProjectRow(sim, 0, 0, 34, "A", sectionActive)
	glyph, _, _ := sim.Get(2, 0)
	testutil.Equal(t, glyph, string(theme.IconNeedsInput))
	tl.ToggleHeraManaged()
	tl.drawProjectRow(sim, 0, 1, 34, "A", sectionActive)
	glyph, _, _ = sim.Get(2, 1)
	testutil.Equal(t, glyph, "○")
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', 0), nil)
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'F', 0), nil)
	testutil.Equal(t, tl.Filter(), "F")
	testutil.Equal(t, tl.ExpandAll(), false)
}

func TestTasksExpandAllModes(t *testing.T) {
	for _, rail := range []bool{false, true} {
		t.Run(fmt.Sprint(rail), func(t *testing.T) {
			tl := NewTaskListView()
			tl.SetRailMode(rail)
			tl.SetTasks([]*model.Task{{ID: "a", Project: "A", Name: "alpha"}, {ID: "b", Project: "B", Name: "beta"}, {ID: "old", Project: "B", Archived: true}})
			tl.SelectByID("a")
			testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"a"})
			var changes []bool
			tl.OnExpandAllToggle = func(v bool) { changes = append(changes, v) }
			tl.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'F', 0), nil)
			testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"a", "b"})
			tl.SelectByID("b")
			tl.SetTasks(tl.tasks)
			testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"a", "b"})
			tl.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'F', 0), nil)
			testutil.Equal(t, tl.SelectedTask().ID, "b")
			testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"b"})
			testutil.DeepEqual(t, changes, []bool{true, false})
		})
	}
}
