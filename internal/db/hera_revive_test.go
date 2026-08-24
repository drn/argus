package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestReviveHeraWorkerToInProgress pins BUG-B: the inverse of
// RollHeraWorkerToReview. A worker-bound task parked in in_review is restored to
// in_progress when it is genuinely revived/working again — UNLESS it is awaiting
// close-out (ready_to_close stamped, or a terminal done/failed role-status), in
// which case it stays in_review (preserving #707 / BUG-050).
func TestReviveHeraWorkerToInProgress(t *testing.T) {
	// setup builds a task at the given status and (optionally) a live binding of
	// the given role kind, returning the DB, the task id, and the bound role id
	// (0 when unbound) so callers can stamp role-status.
	setup := func(t *testing.T, status model.Status, kind HeraRoleKind, bind bool) (*DB, string, int64) {
		t.Helper()
		d := heraTestDB(t)
		task := &model.Task{Name: "t", Status: status, Project: "p"}
		testutil.NoError(t, d.Add(task))
		var roleID int64
		if bind {
			o := mkOrch(t, d, "o")
			r := mkRole(t, d, o.ID, "r", kind)
			mkBinding(t, d, r.ID, task.ID, "/wt/t")
			roleID = r.ID
		}
		return d, task.ID, roleID
	}

	t.Run("worker in_review, no close-out marker -> flips to in_progress", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInReview, HeraKindWorker, true)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, true)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("ready_to_close (done/clean-exit) -> stays in_review", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInReview, HeraKindWorker, true)
		testutil.NoError(t, d.SetMeta(id, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
	})

	t.Run("terminal role-status done -> stays in_review", func(t *testing.T) {
		d, id, roleID := setup(t, model.StatusInReview, HeraKindWorker, true)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusDone))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
	})

	t.Run("terminal role-status failed -> stays in_review", func(t *testing.T) {
		d, id, roleID := setup(t, model.StatusInReview, HeraKindWorker, true)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusFailed))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
	})

	t.Run("non-terminal role-status working -> flips", func(t *testing.T) {
		d, id, roleID := setup(t, model.StatusInReview, HeraKindWorker, true)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusWorking))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, true)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("already in_progress -> no-op", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInProgress, HeraKindWorker, true)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("complete, no close-out marker -> flips to in_progress (add-hera-accept-lifecycle)", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusComplete, HeraKindWorker, true)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, true)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("complete with ready_to_close -> stays complete", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusComplete, HeraKindWorker, true)
		testutil.NoError(t, d.SetMeta(id, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusComplete)
	})

	t.Run("complete with terminal role-status done -> stays complete", func(t *testing.T) {
		d, id, roleID := setup(t, model.StatusComplete, HeraKindWorker, true)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusDone))
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusComplete)
	})

	t.Run("pending -> no-op (still refused, not a valid source)", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusPending, HeraKindWorker, true)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusPending)
	})

	t.Run("coordinator-kind -> no-op", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInReview, HeraKindCoordinator, true)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
	})

	t.Run("no binding -> no-op", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInReview, HeraKindWorker, false)
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
	})

	t.Run("idempotent: second call is a no-op", func(t *testing.T) {
		d, id, _ := setup(t, model.StatusInReview, HeraKindWorker, true)
		_, _ = d.ReviveHeraWorkerToInProgress(id) // now in_progress
		restored, err := d.ReviveHeraWorkerToInProgress(id)
		testutil.NoError(t, err)
		testutil.Equal(t, restored, false)
	})
}

// TestClearHeraCloseout pins add-force-revive-third-enter: unlike
// ReviveHeraWorkerToInProgress (which REFUSES to act on a closed-out
// worker), ClearHeraCloseout is the explicit operator-override path — it
// must clear BOTH of HeraWorkerAwaitingCloseout's signals so the guard
// doesn't immediately re-trip on the freshly-revived session's own next
// natural exit.
func TestClearHeraCloseout(t *testing.T) {
	setup := func(t *testing.T) (*DB, string, int64) {
		t.Helper()
		d := heraTestDB(t)
		task := &model.Task{Name: "t", Status: model.StatusInReview, Project: "p"}
		testutil.NoError(t, d.Add(task))
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		mkBinding(t, d, r.ID, task.ID, "/wt/t")
		return d, task.ID, r.ID
	}

	t.Run("clears ready_to_close meta", func(t *testing.T) {
		d, id, _ := setup(t)
		testutil.NoError(t, d.SetMeta(id, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"))
		testutil.NoError(t, d.ClearHeraCloseout(id))
		awaiting, err := d.HeraWorkerAwaitingCloseout(id)
		testutil.NoError(t, err)
		testutil.Equal(t, awaiting, false)
	})

	t.Run("resets terminal role-status done to working", func(t *testing.T) {
		d, id, roleID := setup(t)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusDone))
		testutil.NoError(t, d.ClearHeraCloseout(id))
		awaiting, err := d.HeraWorkerAwaitingCloseout(id)
		testutil.NoError(t, err)
		testutil.Equal(t, awaiting, false)
		rs, err := d.HeraRoleStatusFor(roleID)
		testutil.NoError(t, err)
		testutil.Equal(t, rs.Status, HeraStatusWorking)
	})

	t.Run("resets terminal role-status failed to working", func(t *testing.T) {
		d, id, roleID := setup(t)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusFailed))
		testutil.NoError(t, d.ClearHeraCloseout(id))
		rs, err := d.HeraRoleStatusFor(roleID)
		testutil.NoError(t, err)
		testutil.Equal(t, rs.Status, HeraStatusWorking)
	})

	t.Run("clears BOTH signals when both present", func(t *testing.T) {
		d, id, roleID := setup(t)
		testutil.NoError(t, d.SetMeta(id, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"))
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusDone))
		testutil.NoError(t, d.ClearHeraCloseout(id))
		awaiting, err := d.HeraWorkerAwaitingCloseout(id)
		testutil.NoError(t, err)
		testutil.Equal(t, awaiting, false)
		rs, err := d.HeraRoleStatusFor(roleID)
		testutil.NoError(t, err)
		testutil.Equal(t, rs.Status, HeraStatusWorking)
	})

	t.Run("non-terminal role-status left alone", func(t *testing.T) {
		d, id, roleID := setup(t)
		testutil.NoError(t, d.UpsertHeraRoleStatus(roleID, HeraStatusBlocked))
		testutil.NoError(t, d.ClearHeraCloseout(id))
		rs, err := d.HeraRoleStatusFor(roleID)
		testutil.NoError(t, err)
		testutil.Equal(t, rs.Status, HeraStatusBlocked)
	})

	t.Run("no role-status row at all -> no-op, no error", func(t *testing.T) {
		d, id, _ := setup(t)
		testutil.NoError(t, d.ClearHeraCloseout(id))
	})

	t.Run("never closed out -> no-op, no error", func(t *testing.T) {
		d, id, _ := setup(t)
		testutil.NoError(t, d.ClearHeraCloseout(id))
		awaiting, err := d.HeraWorkerAwaitingCloseout(id)
		testutil.NoError(t, err)
		testutil.Equal(t, awaiting, false)
	})
}
