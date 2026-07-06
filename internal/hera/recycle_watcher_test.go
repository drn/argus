package hera

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestRecycleWatcher_Tick_RestartsIdlePendingCoordinator(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: true}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, true)
	testutil.Equal(t, runner.restartTaskID, task.ID)

	// Successful restart clears the pending intent so the next tick doesn't
	// re-fire.
	if metaHasValue(t, d, task.ID, db.HeraMetaKeyPendingRecycle, "true") {
		t.Fatalf("pending_recycle was not cleared after a successful restart")
	}
}

func TestRecycleWatcher_Tick_LeavesBusyCoordinatorPending(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: false}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, false)
	if !metaHasValue(t, d, task.ID, db.HeraMetaKeyPendingRecycle, "true") {
		t.Fatalf("pending_recycle was cleared even though the coordinator never went idle")
	}
}

func TestRecycleWatcher_Tick_IgnoresNonCoordinatorPendingFlag(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	orch, err := d.CreateHeraOrchestrator("myorch", "master")
	testutil.NoError(t, err)

	workerTask := &model.Task{Name: "worker-task", Status: model.StatusInProgress, Project: "test-project", Worktree: "/wt/w1"}
	testutil.NoError(t, d.Add(workerTask))
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "w1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   workerTask.Project,
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)

	// A worker somehow ends up with a pending_recycle flag (should never
	// happen via hera_status, which rejects it for non-coordinators) — the
	// watcher must ignore it rather than recycling a worker's session.
	testutil.NoError(t, d.SetMeta(workerTask.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: true}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, false)
}

// concurrentRecycleRunner is a goroutine-safe RecycleRunner fake, distinct
// from fakeRecycleRunner (recycle_test.go) which is unguarded and only safe
// for single-goroutine use — TestRecycleWatcher_StartStop drives Tick from a
// real background goroutine via Start, so reading the outcome from the test
// goroutine needs synchronization to stay -race clean.
type concurrentRecycleRunner struct {
	mu            sync.Mutex
	restartCalled bool
}

func (f *concurrentRecycleRunner) IsIdle(string) bool { return true }
func (f *concurrentRecycleRunner) StopStrayJobs(string, string) error {
	return nil
}
func (f *concurrentRecycleRunner) Restart(taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCalled = true
	return nil
}
func (f *concurrentRecycleRunner) wasRestarted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restartCalled
}

func TestRecycleWatcher_StartStop(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &concurrentRecycleRunner{}
	w := NewRecycleWatcher(d, runner)
	w.SetInterval(10 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		w.Start()
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !runner.wasRestarted() {
		time.Sleep(5 * time.Millisecond)
	}
	testutil.Equal(t, runner.wasRestarted(), true)

	w.Stop()
	w.Stop() // idempotent
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func metaHasValue(t *testing.T, d *db.DB, taskID, key, value string) bool {
	t.Helper()
	meta, err := d.ListMeta(taskID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	for _, e := range meta {
		if e.Key == key && e.Value == value {
			return true
		}
	}
	return false
}
