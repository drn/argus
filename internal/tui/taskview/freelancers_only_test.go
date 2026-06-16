package taskview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// freeTasks returns a mix of freelancer and managed tasks for filter tests.
func freeTasks() ([]*model.Task, map[string]bool) {
	tasks := []*model.Task{
		{ID: "free1", Name: "free-task-1", Project: "p", Status: model.StatusInProgress},
		{ID: "free2", Name: "free-task-2", Project: "p", Status: model.StatusPending},
		{ID: "managed1", Name: "managed-task-1", Project: "p", Status: model.StatusInProgress},
		{ID: "managed2", Name: "managed-task-2", Project: "p", Status: model.StatusPending},
	}
	managed := map[string]bool{
		"managed1": true,
		"managed2": true,
	}
	return tasks, managed
}

// TestFreelancersOnly_DefaultOff confirms the toggle is off by default.
func TestFreelancersOnly_DefaultOff(t *testing.T) {
	tl := NewTaskListView()
	testutil.Equal(t, tl.FreelancersOnly(), false)
}

// TestFreelancersOnly_SetManagedTasks_NilClears confirms nil resets to an empty
// map and does not leave a nil map that could cause panics.
func TestFreelancersOnly_SetManagedTasks_NilClears(t *testing.T) {
	tl := NewTaskListView()
	tl.SetManagedTasks(map[string]bool{"x": true})
	tl.SetManagedTasks(nil)
	// After nil, the managed map should be empty (not nil — no-panic invariant).
	// VisibleTaskIDs still works: no panic.
	tasks := []*model.Task{{ID: "x", Name: "x", Project: "p", Status: model.StatusPending}}
	tl.SetManagedTasks(nil)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)
	tl.freelancersOnly = true
	tl.buildRows()
	// "x" is no longer in the managed set, so it should be visible.
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "x"), true)
}

// TestFreelancersOnly_TogglingOnHidesManagedTasks confirms that when
// freelancersOnly is active, tasks in the managed set are excluded from the
// visible row list; freelancer tasks remain visible.
func TestFreelancersOnly_TogglingOnHidesManagedTasks(t *testing.T) {
	tl := NewTaskListView()
	tasks, managed := freeTasks()
	tl.SetManagedTasks(managed)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	// Default: all tasks visible (filter off).
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "free1"), true)
	testutil.Equal(t, idsContain(vis, "managed1"), true)

	// Toggle on: managed tasks hidden, freelancer tasks remain.
	tl.ToggleFreelancersOnly()
	testutil.Equal(t, tl.FreelancersOnly(), true)
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "free1"), true)
	testutil.Equal(t, idsContain(vis, "free2"), true)
	testutil.Equal(t, idsContain(vis, "managed1"), false)
	testutil.Equal(t, idsContain(vis, "managed2"), false)
}

// TestFreelancersOnly_TogglingOffRestoresManagedTasks confirms the full list is
// restored when the filter is toggled off.
func TestFreelancersOnly_TogglingOffRestoresManagedTasks(t *testing.T) {
	tl := NewTaskListView()
	tasks, managed := freeTasks()
	tl.SetManagedTasks(managed)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	tl.ToggleFreelancersOnly() // on
	tl.ToggleFreelancersOnly() // off
	testutil.Equal(t, tl.FreelancersOnly(), false)

	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "managed1"), true)
	testutil.Equal(t, idsContain(vis, "managed2"), true)
}

// TestFreelancersOnly_TaskWithNoBindingIsFreelancer confirms that a task not in
// the managed set is visible when freelancersOnly is active (scenario: no live
// binding → freelancer).
func TestFreelancersOnly_TaskWithNoBindingIsFreelancer(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "no-binding", Name: "no-binding", Project: "p", Status: model.StatusPending},
	}
	// managed set is empty — no task is managed.
	tl.SetManagedTasks(map[string]bool{})
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	tl.ToggleFreelancersOnly()
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "no-binding"), true)
}

// TestFreelancersOnly_FreelanceKindBindingIsNotManaged confirms that a task not
// in the managed set (i.e., only a freelance-kind binding) remains visible.
func TestFreelancersOnly_FreelanceKindBindingIsNotManaged(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "freelance-bound", Name: "freelance-bound", Project: "p", Status: model.StatusPending},
		{ID: "coord", Name: "coord", Project: "p", Status: model.StatusInProgress},
	}
	// Only "coord" is managed (has a coordinator-kind live binding); "freelance-bound"
	// is NOT in the managed set — it has a freelance-kind binding which doesn't count.
	managed := map[string]bool{"coord": true}
	tl.SetManagedTasks(managed)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	tl.ToggleFreelancersOnly()
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "freelance-bound"), true)
	testutil.Equal(t, idsContain(vis, "coord"), false)
}

// TestFreelancersOnly_FinishedWorkerIsFreelancer confirms that a task whose
// worker binding ended (and is therefore absent from the managed set) is treated
// as a freelancer when freelancersOnly is active.
func TestFreelancersOnly_FinishedWorkerIsFreelancer(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "finished-worker", Name: "finished-worker", Project: "p", Status: model.StatusInReview},
	}
	// "finished-worker" has an ended binding — ManagedTaskIDs() does not include it.
	tl.SetManagedTasks(map[string]bool{}) // empty: binding ended, not managed
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	tl.ToggleFreelancersOnly()
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "finished-worker"), true)
}

// TestFreelancersOnly_ComposesWithSubstringFilter confirms that both exclusions
// apply independently: visible = freelancer AND matches the substring filter.
func TestFreelancersOnly_ComposesWithSubstringFilter(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "free-alpha", Name: "free-alpha", Project: "p", Status: model.StatusPending},
		{ID: "free-other", Name: "free-other", Project: "p", Status: model.StatusPending},
		{ID: "managed-alpha", Name: "managed-alpha", Project: "p", Status: model.StatusPending},
	}
	managed := map[string]bool{"managed-alpha": true}
	tl.SetManagedTasks(managed)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	// Activate freelancers-only + substring filter "alpha".
	tl.ToggleFreelancersOnly()
	tl.filter = "alpha"
	tl.buildRows()

	vis := tl.VisibleTaskIDs()
	// free-alpha: freelancer + matches "alpha" → visible.
	testutil.Equal(t, idsContain(vis, "free-alpha"), true)
	// free-other: freelancer but "other" doesn't match "alpha" → hidden by substring filter.
	testutil.Equal(t, idsContain(vis, "free-other"), false)
	// managed-alpha: matches "alpha" but is managed → hidden by freelancers-only.
	testutil.Equal(t, idsContain(vis, "managed-alpha"), false)
}

// TestFreelancersOnly_ComposesWithHideHeraWorkers confirms that the two
// exclusions (hideHeraWorkers and freelancersOnly) are orthogonal and both apply.
func TestFreelancersOnly_ComposesWithHideHeraWorkers(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "normal", Name: "normal", Project: "p", Status: model.StatusPending},
		{ID: "hera-worker", Name: "hera-worker", Project: "p", Status: model.StatusInProgress},
		{ID: "managed", Name: "managed", Project: "p", Status: model.StatusInProgress},
	}
	// hera-worker is in the heraWorkers set (hidden by `H` by default).
	tl.SetHeraWorkers(map[string]bool{"hera-worker": true})
	// managed is in the managed set (hidden by freelancersOnly).
	tl.SetManagedTasks(map[string]bool{"managed": true})
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	// Default state: hideHeraWorkers=true, freelancersOnly=false.
	vis := tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "normal"), true)
	testutil.Equal(t, idsContain(vis, "hera-worker"), false) // hidden by H
	testutil.Equal(t, idsContain(vis, "managed"), true)      // visible (freelancersOnly off)

	// Enable freelancersOnly: also hides "managed".
	tl.ToggleFreelancersOnly()
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "normal"), true)
	testutil.Equal(t, idsContain(vis, "hera-worker"), false) // still hidden by H
	testutil.Equal(t, idsContain(vis, "managed"), false)     // now hidden by freelancersOnly

	// Reveal hera workers with H — still does not un-hide managed.
	tl.ToggleHeraWorkers() // H: now reveals hera workers
	vis = tl.VisibleTaskIDs()
	testutil.Equal(t, idsContain(vis, "hera-worker"), true) // now visible
	testutil.Equal(t, idsContain(vis, "managed"), false)    // still hidden by freelancersOnly
}

// TestFreelancersOnly_ToggleFiresCallback confirms OnFreelancersOnlyToggle fires
// with the new state on each toggle.
func TestFreelancersOnly_ToggleFiresCallback(t *testing.T) {
	tl := NewTaskListView()
	var got []bool
	tl.OnFreelancersOnlyToggle = func(active bool) { got = append(got, active) }
	tl.ToggleFreelancersOnly() // → true
	tl.ToggleFreelancersOnly() // → false
	testutil.DeepEqual(t, got, []bool{true, false})
}

// TestFreelancersOnly_FKeyBinding confirms the `f` key triggers the toggle.
func TestFreelancersOnly_FKeyBinding(t *testing.T) {
	tl := NewTaskListView()
	tasks, managed := freeTasks()
	tl.SetManagedTasks(managed)
	tl.SetExpanded("p")
	tl.SetTasks(tasks)

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone), func(p tview.Primitive) {})
	testutil.Equal(t, tl.FreelancersOnly(), true)

	handler(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone), func(p tview.Primitive) {})
	testutil.Equal(t, tl.FreelancersOnly(), false)
}

// TestFreelancersOnly_TitleIndicator confirms that when freelancersOnly is true
// the panel title contains the indicator text, and that it can coexist with the
// `/filter` indicator.
func TestFreelancersOnly_TitleIndicator(t *testing.T) {
	sim := newSim(t, 80, 12)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task-a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.freelancersOnly = true
	tl.SetRect(0, 0, 80, 12)
	tl.Draw(sim)

	out := dumpScreen(sim)
	if !strings.Contains(out, "freelancers") {
		t.Errorf("title should contain freelancers indicator when freelancersOnly=true; got: %q", out)
	}
}

// TestFreelancersOnly_TitleIndicator_WithFilter confirms that both the
// `/filter` indicator and the freelancers-only indicator are present when both
// are active simultaneously.
func TestFreelancersOnly_TitleIndicator_WithFilter(t *testing.T) {
	sim := newSim(t, 80, 12)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task-a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.freelancersOnly = true
	tl.filter = "task"
	tl.SetRect(0, 0, 80, 12)
	tl.Draw(sim)

	out := dumpScreen(sim)
	// Substring filter indicator.
	if !strings.Contains(out, "/task") {
		t.Errorf("title should contain filter indicator; got: %q", out)
	}
	// Freelancers-only indicator — distinct from the filter indicator.
	if !strings.Contains(out, "freelancers") {
		t.Errorf("title should contain freelancers indicator; got: %q", out)
	}
}

// TestFreelancersOnly_TitleIndicator_Absent confirms no freelancers indicator
// when freelancersOnly is false.
func TestFreelancersOnly_TitleIndicator_Absent(t *testing.T) {
	sim := newSim(t, 80, 12)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task-a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.freelancersOnly = false
	tl.SetRect(0, 0, 80, 12)
	tl.Draw(sim)

	out := dumpScreen(sim)
	if strings.Contains(out, "freelancers") {
		t.Errorf("title should NOT contain freelancers indicator when freelancersOnly=false; got: %q", out)
	}
}
