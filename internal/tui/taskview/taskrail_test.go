package taskview

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestTaskRailFocusFoldAndArchive(t *testing.T) {
	tl := NewTaskListView()
	tl.SetRailMode(true)
	tl.SetTasks([]*model.Task{{ID: "a", Project: "A"}, {ID: "b", Project: "B"}, {ID: "c", Project: "A", Archived: true}, {ID: "p", Project: "P", Pinned: true}})
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"p", "a"})
	tl.SelectByID("a")
	tl.CursorUp()
	testutil.Nil(t, tl.SelectedTask())
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', 0), nil)
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"p", "a"})
	tl.CursorDown() // A task
	tl.CursorDown() // B header
	tl.CursorDown() // B task
	testutil.Equal(t, tl.SelectedTask().ID, "b")
	tl.SetTasks(tl.tasks)
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"p", "b"})
	tl.CursorDown() // Archive
	testutil.Nil(t, tl.SelectedTask())
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"p", "b", "c"})
	tl.SelectByID("c")
	tl.SelectByID("a")
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"p", "a", "c"})
}

func TestTaskRailFilterAndLegacyIsolation(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{{ID: "a", Project: "A", Name: "first"}, {ID: "b", Project: "B", Name: "worker"}}
	tl.SetTasks(tasks)
	tl.SelectByID("a")
	legacy := tl.VisibleTaskIDs()
	tl.SetRailMode(true)
	tl.SetManagedTasks(map[string]bool{"b": true})
	tl.ToggleHeraManaged()
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"a"})
	testutil.Nil(t, tl.AdjacentTask("a", 1))
	tl.ToggleHeraManaged()
	tl.SetRailMode(false)
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), legacy)
}

func TestTaskRailEnterCompleteAndHeader(t *testing.T) {
	tl := NewTaskListView()
	tl.SetRailMode(true)
	tl.SetTasks([]*model.Task{{ID: "done", Project: "A", Status: model.StatusComplete}})
	selected := ""
	tl.OnSelect = func(task *model.Task) { selected = task.ID }
	tl.SelectByID("done")
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.Equal(t, selected, "done")
	tl.CursorUp()
	tl.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"done"})
}
