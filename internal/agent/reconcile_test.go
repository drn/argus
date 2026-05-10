package agent

// Tests for reconcile.go.

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)


// TestReconcileStaleSessions_FlipsInProgressToInReview verifies the function
// flips InProgress rows to InReview (the post-restart drift recovery).
func TestReconcileStaleSessions_FlipsInProgressToInReview(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	stale := &model.Task{Name: "stale-1", Project: "proj", Status: model.StatusInProgress}
	if err := d.Add(stale); err != nil {
		t.Fatal(err)
	}
	pending := &model.Task{Name: "pending-1", Project: "proj", Status: model.StatusPending}
	if err := d.Add(pending); err != nil {
		t.Fatal(err)
	}
	complete := &model.Task{Name: "complete-1", Project: "proj", Status: model.StatusComplete}
	if err := d.Add(complete); err != nil {
		t.Fatal(err)
	}

	count, err := ReconcileStaleSessions(d)
	testutil.NoError(t, err)
	testutil.Equal(t, count, 1)

	got, err := d.Get(stale.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)

	got, err = d.Get(pending.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusPending)

	got, err = d.Get(complete.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
}

// TestReconcileStaleSessions_NoInProgress is a no-op when nothing is stale.
func TestReconcileStaleSessions_NoInProgress(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	pending := &model.Task{Name: "p", Project: "proj", Status: model.StatusPending}
	if err := d.Add(pending); err != nil {
		t.Fatal(err)
	}

	count, err := ReconcileStaleSessions(d)
	testutil.NoError(t, err)
	testutil.Equal(t, count, 0)
}

// TestReconcileStaleSessions_TasksError exercises the error path when the
// underlying Tasks call fails. Closes the DB so Tasks errors out.
func TestReconcileStaleSessions_TasksError(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	d.Close()

	_, err = ReconcileStaleSessions(d)
	if err == nil {
		t.Fatal("expected error when DB is closed")
	}
}
