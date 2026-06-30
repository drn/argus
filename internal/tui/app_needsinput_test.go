package tui

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestNeedsInputInProgress_GatesOnStatus proves the Hera needs-input feed drops
// sticky entries for tasks that are no longer in_progress (BUG-005), matching the
// task list and buildRoleView gates, while preserving order for the ones kept.
func TestNeedsInputInProgress_GatesOnStatus(t *testing.T) {
	tasks := []*model.Task{
		{ID: "live", Status: model.StatusInProgress},
		{ID: "done", Status: model.StatusComplete},
		{ID: "review", Status: model.StatusInReview},
		{ID: "live2", Status: model.StatusInProgress},
	}
	// Sticky set carries finished tasks ("done", "review") plus a totally-unknown id.
	got := needsInputInProgress([]string{"done", "live", "review", "live2", "ghost"}, tasks)
	testutil.DeepEqual(t, got, []string{"live", "live2"})
}

func TestNeedsInputInProgress_EmptyInput(t *testing.T) {
	testutil.Equal(t, len(needsInputInProgress(nil, []*model.Task{{ID: "x", Status: model.StatusInProgress}})), 0)
}

func TestNeedsInputInProgress_NoInProgressTasks(t *testing.T) {
	tasks := []*model.Task{{ID: "a", Status: model.StatusComplete}}
	testutil.Equal(t, len(needsInputInProgress([]string{"a"}, tasks)), 0)
}

// TestNeedsInputForHeraRail_AdmitsHeraRolesRegardlessOfStatus is the admission
// step for BUG-028 (coordinators) AND BUG-A (workers): the Hera rail feed keeps
// in_progress tasks AND any task bound to ANY hera role — coordinator or worker —
// even when its task is complete/in_review. A coordinator commonly rolls to a
// terminal status while still alive and blocked (BUG-028); a worker sits in
// in_review while its session lingers for close-out and can genuinely ask there
// (BUG-A, #707). A task bound to no hera role and not in_progress is dropped.
// buildRoleView re-gates each admitted task on its LIVE binding, so an exited
// worker (ended binding) is still suppressed there (BUG-023).
func TestNeedsInputForHeraRail_AdmitsHeraRolesRegardlessOfStatus(t *testing.T) {
	tasks := []*model.Task{
		{ID: "wkr-live", Status: model.StatusInProgress},
		{ID: "wkr-review", Status: model.StatusInReview},
		{ID: "coord-complete", Status: model.StatusComplete},
		{ID: "plain-done", Status: model.StatusComplete},
	}
	// heraManaged is the union of worker + coordinator meta sets (any hera-bound
	// task, regardless of liveness — buildRoleView re-gates on the live binding).
	heraManaged := map[string]bool{"wkr-live": true, "wkr-review": true, "coord-complete": true}
	got := needsInputForHeraRail(
		[]string{"plain-done", "wkr-live", "wkr-review", "coord-complete", "ghost"},
		tasks, heraManaged,
	)
	// wkr-live kept (in_progress + managed); wkr-review kept (managed worker,
	// in_review — BUG-A); coord-complete kept (managed coordinator — BUG-028);
	// plain-done dropped (not in_progress, not hera-bound); ghost dropped (unknown).
	// Order preserved.
	testutil.DeepEqual(t, got, []string{"wkr-live", "wkr-review", "coord-complete"})
}

func TestNeedsInputForHeraRail_EmptyInput(t *testing.T) {
	testutil.Equal(t, len(needsInputForHeraRail(nil, nil, map[string]bool{"c": true})), 0)
}
