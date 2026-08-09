package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
)

// TestHeraReclaimAndArchiveTask_InReviewAdvancesToComplete pins the
// fix-hera-archive-status behavior: a task already resting at in_review when
// it's reclaimed+archived is also advanced to complete, mirroring
// RollHeraWorkerToReview's own never-clobber-active-work invariant one step
// later in the chain.
func TestHeraReclaimAndArchiveTask_InReviewAdvancesToComplete(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.SetStatus("tw", model.StatusInReview))

	reclaimed := app.heraReclaimAndArchiveTask("tw")

	testutil.Equal(t, reclaimed, false) // no worktree/branch on this task, nothing to reclaim
	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusComplete)
}

// TestHeraReclaimAndArchiveTask_InProgressStatusUntouched verifies a
// still-active task (e.g. an operator force-nuking a live, still-working
// role) is archived exactly as before, WITHOUT being forced to complete.
func TestHeraReclaimAndArchiveTask_InProgressStatusUntouched(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	app.heraReclaimAndArchiveTask("tw")

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusInProgress) // untouched, NOT complete
}

// TestHeraReclaimAndArchiveTask_PendingStatusUntouched verifies a never-run
// task is archived without ever being marked complete.
func TestHeraReclaimAndArchiveTask_PendingStatusUntouched(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))

	app.heraReclaimAndArchiveTask("tw")

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusPending) // untouched, NOT complete
}

// TestHeraReclaimAndArchiveTask_AlreadyCompleteIdempotent verifies reclaiming
// an already-complete task is a status no-op (still archives cleanly).
func TestHeraReclaimAndArchiveTask_AlreadyCompleteIdempotent(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.SetStatus("tw", model.StatusInReview))
	testutil.NoError(t, d.SetStatus("tw", model.StatusComplete))

	app.heraReclaimAndArchiveTask("tw")

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusComplete)
}

// TestHeraNukeRole_MultiBoundLeavesStatusUntouched verifies the outer
// multi-binding guard (heraTaskSolelyBoundTo) prevents heraReclaimAndArchiveTask
// from ever running for a task bound live under more than one orchestrator —
// so neither its archived flag NOR its status are touched, even when the task
// is sitting in_review.
func TestHeraNukeRole_MultiBoundLeavesStatusUntouched(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	a := seedHeraOrch(t, d, "A")
	b := seedHeraOrch(t, d, "B")
	roleA := seedHeraBoundRole(t, d, a, "w", db.HeraKindWorker, "shared")
	testutil.NoError(t, d.SetStatus("shared", model.StatusInReview))
	roleB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: b, Name: "c", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: "shared", WorktreePath: "/wt/shared2"})
	testutil.NoError(t, err)

	rv := &hera.RoleView{RoleID: roleA.ID, OrchID: a, Kind: db.HeraKindWorker, TaskID: "shared", Live: true}
	app.heraNukeRole(rv)

	task, err := d.Get("shared")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, false)
	testutil.Equal(t, task.Status, model.StatusInReview) // untouched — never reclaimed
}

// TestHeraDoCascadeNuke_StatusAdvanceIsUniformAcrossRoleKinds pins the
// role-kind-agnostic part of the design: a cascade nuke over a subtree
// containing both a coordinator (still in_progress) and a worker (already
// in_review) advances ONLY the in_review task to complete — the decision is
// keyed on each task's own status column, not on the hera role's kind.
func TestHeraDoCascadeNuke_StatusAdvanceIsUniformAcrossRoleKinds(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc") // stays in_progress
	seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.SetStatus("tw", model.StatusInReview))
	app.heraPage.Refresh()

	subtree := app.heraPage.Rail().Model().BridgeSubtree(orch)
	subtreeIDs := map[int64]bool{orch: true}
	app.heraDoCascadeNuke(subtree, subtreeIDs)

	tc, err := d.Get("tc")
	testutil.NoError(t, err)
	testutil.Equal(t, tc.Archived, true)
	testutil.Equal(t, tc.Status, model.StatusInProgress) // coordinator: still active, untouched

	tw, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, tw.Archived, true)
	testutil.Equal(t, tw.Status, model.StatusComplete) // worker: was in_review, now complete
}
