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

// TestReconcileStaleSessionsExcept_SkipsAliveFlipsOrphans is the supervisor-mode
// (P3) reconcile: tasks in the alive set (re-attached, kept running by the
// supervisor across the daemon bounce) stay InProgress; InProgress tasks NOT in
// the set are true orphans and flip to InReview. The returned slice is exactly
// the orphans, so the caller can signal ARGUS_BOUNCED to just them.
func TestReconcileStaleSessionsExcept_SkipsAliveFlipsOrphans(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	alive := &model.Task{Name: "alive", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(alive))
	orphan := &model.Task{Name: "orphan", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(orphan))
	done := &model.Task{Name: "done", Project: "proj", Status: model.StatusComplete}
	testutil.NoError(t, d.Add(done))

	flipped, err := ReconcileStaleSessionsExcept(d, map[string]bool{alive.ID: true})
	testutil.NoError(t, err)
	testutil.DeepEqual(t, flipped, []string{orphan.ID})

	got, err := d.Get(alive.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInProgress) // re-attached — not flipped

	got, err = d.Get(orphan.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview) // true orphan — flipped

	got, err = d.Get(done.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete) // untouched
}

// TestReconcileStaleSessionsExcept_NilAliveFlipsAll proves a nil alive set is
// equivalent to the in-process ReconcileStaleSessions (every InProgress flips).
func TestReconcileStaleSessionsExcept_NilAliveFlipsAll(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	a := &model.Task{Name: "a", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(a))
	b := &model.Task{Name: "b", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(b))

	flipped, err := ReconcileStaleSessionsExcept(d, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(flipped), 2)

	for _, id := range []string{a.ID, b.ID} {
		got, gerr := d.Get(id)
		testutil.NoError(t, gerr)
		testutil.Equal(t, got.Status, model.StatusInReview)
	}
}
