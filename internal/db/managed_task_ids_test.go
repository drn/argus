package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// mkLiveBinding is a thin helper: creates a binding with ended_at IS NULL for
// the given role and task. WorktreePath is made unique per call to avoid
// violating the per-(worktree,orchestrator) live-uniqueness index.
func mkLiveBinding(t *testing.T, d *DB, roleID int64, taskID, worktreePath string) {
	t.Helper()
	_, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID:       roleID,
		ArgusTaskID:  taskID,
		WorktreePath: worktreePath,
	})
	testutil.NoError(t, err)
}

// mkEndedBinding creates a binding then immediately ends it, simulating a
// task whose coordination is complete.
func mkEndedBinding(t *testing.T, d *DB, roleID int64, taskID, worktreePath string) {
	t.Helper()
	b, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID:       roleID,
		ArgusTaskID:  taskID,
		WorktreePath: worktreePath,
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(b.ID, "test-ended"))
}

func TestManagedTaskIDs(t *testing.T) {
	t.Run("task with live worker binding is present", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "w", HeraKindWorker)
		mkLiveBinding(t, d, r.ID, "task-worker", "/wt/worker")

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got["task-worker"], true)
	})

	t.Run("task with live coordinator binding is present", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		mkLiveBinding(t, d, r.ID, "task-coord", "/wt/coord")

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got["task-coord"], true)
	})

	t.Run("task with only a live freelance binding is absent", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "free", HeraKindFreelance)
		mkLiveBinding(t, d, r.ID, "task-free", "/wt/free")

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got["task-free"], false)
	})

	t.Run("task with ended worker binding is absent", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "w", HeraKindWorker)
		mkEndedBinding(t, d, r.ID, "task-ended", "/wt/ended")

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got["task-ended"], false)
	})

	t.Run("task with no binding at all is absent", func(t *testing.T) {
		d := heraTestDB(t)
		// No orchestrators or bindings seeded.

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got["task-nobinding"], false)
	})

	t.Run("empty DB returns empty non-nil map", func(t *testing.T) {
		d := heraTestDB(t)

		got, err := d.ManagedTaskIDs()
		testutil.NoError(t, err)
		testutil.Equal(t, got != nil, true)
		testutil.Equal(t, len(got), 0)
	})
}
