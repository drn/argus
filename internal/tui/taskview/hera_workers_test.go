package taskview

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func idsContain(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestHideHeraWorkers_DefaultHidesAndToggleReveals(t *testing.T) {
	tl := NewTaskListView()
	testutil.Equal(t, tl.HideHeraWorkers(), true) // hidden by default

	tasks := []*model.Task{
		{ID: "normal", Name: "normal task", Project: "p", Status: model.StatusInProgress},
		{ID: "worker", Name: "hera worker", Project: "p", Status: model.StatusInProgress},
	}
	tl.SetHeraWorkers(map[string]bool{"worker": true})
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	// Default: the hera worker is hidden, the normal task is shown.
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "normal"), true)
	testutil.Equal(t, idsContain(vis, "worker"), false)

	// Reveal toggle (`H`): the worker now appears.
	tl.ToggleHeraWorkers()
	testutil.Equal(t, tl.HideHeraWorkers(), false)
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "worker"), true)

	// Toggle back hides it again.
	tl.ToggleHeraWorkers()
	testutil.Equal(t, idsContain(tl.VisibleTaskIDs(), "worker"), false)
}

func TestHideHeraWorkers_ToggleFiresCallback(t *testing.T) {
	tl := NewTaskListView()
	var got []bool
	tl.OnHeraWorkersToggle = func(hidden bool) { got = append(got, hidden) }
	tl.ToggleHeraWorkers() // → false
	tl.ToggleHeraWorkers() // → true
	testutil.DeepEqual(t, got, []bool{false, true})
}

func TestSetHeraWorkers_NilClears(t *testing.T) {
	tl := NewTaskListView()
	tl.SetHeraWorkers(map[string]bool{"w": true})
	testutil.Equal(t, tl.isHeraSpawnedWorker(&model.Task{ID: "w"}), true)
	tl.SetHeraWorkers(nil)
	testutil.Equal(t, tl.isHeraSpawnedWorker(&model.Task{ID: "w"}), false)
}

func TestSetHeraCoordinators_SetAndCheck(t *testing.T) {
	tl := NewTaskListView()
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "c"}), false)
	tl.SetHeraCoordinators(map[string]bool{"c": true})
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "c"}), true)
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "other"}), false)
}

func TestSetHeraCoordinators_NilClears(t *testing.T) {
	tl := NewTaskListView()
	tl.SetHeraCoordinators(map[string]bool{"c": true})
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "c"}), true)
	tl.SetHeraCoordinators(nil)
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "c"}), false)
}

// Coordinator status is independent of worker status: a task flagged a
// coordinator is not hidden as a worker, and the two sets don't bleed.
func TestHeraCoordinator_OrthogonalToWorker(t *testing.T) {
	tl := NewTaskListView()
	tl.SetHeraWorkers(map[string]bool{"w": true})
	tl.SetHeraCoordinators(map[string]bool{"c": true})
	testutil.Equal(t, tl.isHeraCoordinator(&model.Task{ID: "w"}), false)
	testutil.Equal(t, tl.isHeraSpawnedWorker(&model.Task{ID: "c"}), false)
}
