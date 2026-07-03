package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// bindReviveWorker creates an orchestrator + worker role + live binding for
// taskID directly on the TUI's local *db.DB.
func bindReviveWorker(t *testing.T, d *db.DB, taskID string) {
	t.Helper()
	o, err := d.CreateHeraOrchestrator("o-"+taskID, "")
	testutil.NoError(t, err)
	r, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "proj"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
}

// TestReviveRestoreInProgress pins the App-side wiring of BUG-B: a successful
// in-place worker revive restores the task to in_progress via the shared
// *db.DB helper (local mode), while a genuinely-finished worker (ready_to_close)
// is left in in_review.
func TestReviveRestoreInProgress(t *testing.T) {
	t.Run("stranded worker restored to in_progress", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)

		task := &model.Task{Name: "stranded", Status: model.StatusInReview, Worktree: "/wt/x"}
		testutil.NoError(t, d.Add(task))
		bindReviveWorker(t, d, task.ID)

		app.reviveRestoreInProgress(task.ID)

		got, _ := d.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("ready_to_close worker stays in_review", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)

		task := &model.Task{Name: "done", Status: model.StatusInReview, Worktree: "/wt/y"}
		testutil.NoError(t, d.Add(task))
		bindReviveWorker(t, d, task.ID)
		testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

		app.reviveRestoreInProgress(task.ID)

		got, _ := d.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInReview)
	})
}
