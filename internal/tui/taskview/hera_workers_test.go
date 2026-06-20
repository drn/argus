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

func TestHideHeraManaged_DefaultHidesAndToggleReveals(t *testing.T) {
	tl := NewTaskListView()
	testutil.Equal(t, tl.HideHeraManaged(), true) // hidden by default

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
	tl.ToggleHeraManaged()
	testutil.Equal(t, tl.HideHeraManaged(), false)
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "worker"), true)

	// Toggle back hides it again.
	tl.ToggleHeraManaged()
	testutil.Equal(t, idsContain(tl.VisibleTaskIDs(), "worker"), false)
}

func TestHideHeraManaged_ToggleFiresCallback(t *testing.T) {
	tl := NewTaskListView()
	var got []bool
	tl.OnHeraManagedToggle = func(hidden bool) { got = append(got, hidden) }
	tl.ToggleHeraManaged() // → false
	tl.ToggleHeraManaged() // → true
	testutil.DeepEqual(t, got, []bool{false, true})
}

// TestHideHeraManaged_TruthTable pins the collapsed single-`H` semantics
// (BUG-025): one toggle hides every hera-managed role that lives in the Hera
// tab — spawned workers (task_meta hera.role=worker) AND live coordinators (a
// live coordinator/worker binding fed via SetManagedTasks). Freelancers and
// plain non-hera tasks stay visible regardless of `H`.
func TestHideHeraManaged_TruthTable(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "worker", Name: "spawned worker", Project: "p", Status: model.StatusInProgress},
		{ID: "coord", Name: "coordinator", Project: "p", Status: model.StatusInProgress},
		{ID: "free", Name: "freelancer", Project: "p", Status: model.StatusInProgress},
		{ID: "plain", Name: "plain task", Project: "p", Status: model.StatusInProgress},
	}
	// "worker" is a hera-spawned worker (permanent task_meta signal). "coord"
	// holds a live coordinator binding (the live `managed` signal the removed
	// freelancers-only filter consumed) — NOT in the spawned-worker set. "free"
	// and "plain" hold no hera binding.
	tl.SetHeraWorkers(map[string]bool{"worker": true})
	tl.SetManagedTasks(map[string]bool{"coord": true, "worker": true})
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	// H on (default): both the worker and the coordinator are hidden; the
	// freelancer and the plain task remain visible.
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, tl.HideHeraManaged(), true)
	testutil.Equal(t, idsContain(vis, "worker"), false)
	testutil.Equal(t, idsContain(vis, "coord"), false)
	testutil.Equal(t, idsContain(vis, "free"), true)
	testutil.Equal(t, idsContain(vis, "plain"), true)

	// H off: every task becomes visible.
	tl.ToggleHeraManaged()
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "worker"), true)
	testutil.Equal(t, idsContain(vis, "coord"), true)
	testutil.Equal(t, idsContain(vis, "free"), true)
	testutil.Equal(t, idsContain(vis, "plain"), true)
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
