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
