package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mkBinding starts a live binding for (role, task) and fails on error.
func mkBinding(t *testing.T, d *DB, roleID int64, taskID, worktree string) *HeraBinding {
	t.Helper()
	b, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID:       roleID,
		ArgusTaskID:  taskID,
		WorktreePath: worktree,
	})
	testutil.NoError(t, err)
	return b
}

func TestTaskHoldsLiveHeraWorkerBinding(t *testing.T) {
	t.Run("no binding is false", func(t *testing.T) {
		d := heraTestDB(t)
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-none")
		testutil.NoError(t, err)
		testutil.Equal(t, got, false)
	})

	t.Run("live worker binding is true", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "w1", HeraKindWorker)
		mkBinding(t, d, r.ID, "task-w", "/wt/w")
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-w")
		testutil.NoError(t, err)
		testutil.Equal(t, got, true)
	})

	t.Run("coordinator binding does not count", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		mkBinding(t, d, r.ID, "task-c", "/wt/c")
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-c")
		testutil.NoError(t, err)
		testutil.Equal(t, got, false)
	})

	t.Run("freelance binding does not count", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "free", HeraKindFreelance)
		mkBinding(t, d, r.ID, "task-f", "/wt/f")
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-f")
		testutil.NoError(t, err)
		testutil.Equal(t, got, false)
	})

	t.Run("ended worker binding is false", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "w1", HeraKindWorker)
		b := mkBinding(t, d, r.ID, "task-w", "/wt/w")
		testutil.NoError(t, d.EndHeraBinding(b.ID, "done"))
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-w")
		testutil.NoError(t, err)
		testutil.Equal(t, got, false)
	})

	t.Run("multi-binding: worker in B counts even when coordinator in A", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "orchA")
		ob := mkOrch(t, d, "orchB")
		coord := mkRole(t, d, oa.ID, "coord", HeraKindCoordinator)
		worker := mkRole(t, d, ob.ID, "worker", HeraKindWorker)
		// Same argus task bound as coordinator in A and worker in B.
		mkBinding(t, d, coord.ID, "task-x", "/wt/x")
		mkBinding(t, d, worker.ID, "task-x", "/wt/x")
		got, err := d.TaskHoldsLiveHeraWorkerBinding("task-x")
		testutil.NoError(t, err)
		testutil.Equal(t, got, true)
	})
}

func TestUniqueHeraRoleName(t *testing.T) {
	t.Run("free base returned unchanged", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		got, err := d.UniqueHeraRoleName(o.ID, "fix-bug")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "fix-bug")
	})

	t.Run("empty base defaults to worker", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		got, err := d.UniqueHeraRoleName(o.ID, "")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "worker")
	})

	t.Run("collision suffixes -2 then -3", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		mkRole(t, d, o.ID, "fix-bug", HeraKindWorker)
		got, err := d.UniqueHeraRoleName(o.ID, "fix-bug")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "fix-bug-2")

		mkRole(t, d, o.ID, "fix-bug-2", HeraKindWorker)
		got, err = d.UniqueHeraRoleName(o.ID, "fix-bug")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "fix-bug-3")
	})

	t.Run("archived sibling does not block reuse", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "fix-bug", HeraKindWorker)
		testutil.NoError(t, d.ArchiveHeraRole(r.ID))
		// Archived role no longer occupies the active-name index → base is free.
		got, err := d.UniqueHeraRoleName(o.ID, "fix-bug")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "fix-bug")
	})

	t.Run("uniqueness is scoped per orchestrator", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "orchA")
		ob := mkOrch(t, d, "orchB")
		mkRole(t, d, oa.ID, "fix-bug", HeraKindWorker)
		// Same name is free under a different orchestrator.
		got, err := d.UniqueHeraRoleName(ob.ID, "fix-bug")
		testutil.NoError(t, err)
		testutil.Equal(t, got, "fix-bug")
	})
}

func TestRollHeraWorkerToReview(t *testing.T) {
	setup := func(t *testing.T, status model.Status, kind HeraRoleKind, bind bool) (*DB, string) {
		t.Helper()
		d := heraTestDB(t)
		task := &model.Task{Name: "t", Status: status, Project: "p"}
		testutil.NoError(t, d.Add(task))
		if bind {
			o := mkOrch(t, d, "o")
			r := mkRole(t, d, o.ID, "r", kind)
			mkBinding(t, d, r.ID, task.ID, "/wt/t")
		}
		return d, task.ID
	}

	t.Run("worker in_progress -> flips + stamps + true", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, true)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
		meta, _ := d.ListMeta(id, HeraMetaNamespace)
		found := false
		for _, e := range meta {
			if e.Key == HeraMetaKeyReadyToClose && e.Value == "true" {
				found = true
			}
		}
		testutil.Equal(t, found, true)
	})

	t.Run("idempotent: second call is a no-op", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, true)
		_, _ = d.RollHeraWorkerToReview(id)
		flipped, err := d.RollHeraWorkerToReview(id) // now in_review
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})

	t.Run("non-worker (coordinator) -> no-op", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindCoordinator, true)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("no binding -> no-op", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, false)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})

	t.Run("already complete -> not clobbered", func(t *testing.T) {
		d, id := setup(t, model.StatusComplete, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusComplete)
	})

	t.Run("human-set in_review -> not re-flipped", func(t *testing.T) {
		d, id := setup(t, model.StatusInReview, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})
}
