package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestHeraRoleStatus_Failed verifies that "failed" round-trips through
// UpsertHeraRoleStatus / HeraRoleStatusFor and is accepted by the CHECK
// constraint (schema.go).
func TestHeraRoleStatus_Failed(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "w", HeraKindWorker)

	testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusFailed))
	got, err := d.HeraRoleStatusFor(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, HeraStatusFailed)

	// Overwrite back to working — idempotent round-trip.
	testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusWorking))
	got2, err := d.HeraRoleStatusFor(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got2.Status, HeraStatusWorking)
}

// TestRollHeraWorkerFailed covers the D2 invariants for the failed roll helper:
//   - in_progress worker flips to in_review WITHOUT ready_to_close
//   - non-in_progress is a no-op (idempotent)
//   - non-worker (coordinator) is a no-op
//   - no binding is a no-op
//   - second call is a no-op (idempotent)
func TestRollHeraWorkerFailed(t *testing.T) {
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

	readyToClose := func(d *DB, id string) bool {
		meta, _ := d.ListMeta(id, HeraMetaNamespace)
		for _, e := range meta {
			if e.Key == HeraMetaKeyReadyToClose {
				return e.Value == "true"
			}
		}
		return false
	}

	t.Run("worker in_progress -> flips to in_review, no ready_to_close", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, true)

		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInReview)
		// D2 key invariant: ready_to_close MUST NOT be set for a failed roll.
		testutil.Equal(t, readyToClose(d, id), false)
	})

	t.Run("idempotent: second call is a no-op (task already in_review)", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, true)
		_, _ = d.RollHeraWorkerFailed(id)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})

	t.Run("non-worker (coordinator) -> no-op", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindCoordinator, true)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("no binding -> no-op", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, false)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})

	t.Run("already complete -> not clobbered", func(t *testing.T) {
		d, id := setup(t, model.StatusComplete, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
		got, _ := d.Get(id)
		testutil.Equal(t, got.Status, model.StatusComplete)
	})

	t.Run("human-set in_review -> not re-flipped", func(t *testing.T) {
		d, id := setup(t, model.StatusInReview, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerFailed(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, false)
	})

	// Contrast with RollHeraWorkerToReview: the done roll DOES stamp ready_to_close.
	t.Run("contrast: done roll stamps ready_to_close, failed roll does not", func(t *testing.T) {
		d, id := setup(t, model.StatusInProgress, HeraKindWorker, true)
		flipped, err := d.RollHeraWorkerToReview(id)
		testutil.NoError(t, err)
		testutil.Equal(t, flipped, true)
		testutil.Equal(t, readyToClose(d, id), true)

		// Reset task status so we can test the failed path independently.
		d2, id2 := setup(t, model.StatusInProgress, HeraKindWorker, true)
		flipped2, err2 := d2.RollHeraWorkerFailed(id2)
		testutil.NoError(t, err2)
		testutil.Equal(t, flipped2, true)
		testutil.Equal(t, readyToClose(d2, id2), false)
	})
}
