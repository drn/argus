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

// TestNeedsInputForHeraRail_AdmitsCoordinatorsRegardlessOfStatus is the BUG-028
// admission step: the Hera rail feed keeps in_progress tasks AND any task bound to
// a hera coordinator role even when its task is complete/in_review (a coordinator
// commonly rolls to a terminal status while still alive and blocked). A finished
// WORKER (not a coordinator) is still dropped — buildRoleView's worker gate
// (BUG-023) and this admission agree.
func TestNeedsInputForHeraRail_AdmitsCoordinatorsRegardlessOfStatus(t *testing.T) {
	tasks := []*model.Task{
		{ID: "wkr-live", Status: model.StatusInProgress},
		{ID: "wkr-done", Status: model.StatusComplete},
		{ID: "coord-complete", Status: model.StatusComplete},
		{ID: "coord-review", Status: model.StatusInReview},
	}
	coordinators := map[string]bool{"coord-complete": true, "coord-review": true}
	got := needsInputForHeraRail(
		[]string{"wkr-done", "wkr-live", "coord-complete", "coord-review", "ghost"},
		tasks, coordinators,
	)
	// wkr-live kept (in_progress); both coordinators kept (regardless of status);
	// wkr-done dropped (finished worker); ghost dropped (unknown). Order preserved.
	testutil.DeepEqual(t, got, []string{"wkr-live", "coord-complete", "coord-review"})
}

func TestNeedsInputForHeraRail_EmptyInput(t *testing.T) {
	testutil.Equal(t, len(needsInputForHeraRail(nil, nil, map[string]bool{"c": true})), 0)
}
