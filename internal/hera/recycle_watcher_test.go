package hera

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
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

// TestRecycleWatcher_Tick_RestartsIdlePendingWorker pins design.md's "Self-
// service recycle works for a worker role" scenario (add-worker-bounce):
// RecycleWatcher.tickTask no longer filters to coordinator-kind bindings
// only, so a worker role's pending_recycle request drives it through
// RecycleCoord's self-service path exactly like a coordinator's.
func TestRecycleWatcher_Tick_RestartsIdlePendingWorker(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "myorch", "w1", "/wt/w1", "argus/w1-branch", "mission")
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: true}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, true)
	testutil.Equal(t, runner.restartTaskID, task.ID)

	if metaHasValue(t, d, task.ID, db.HeraMetaKeyPendingRecycle, "true") {
		t.Fatalf("pending_recycle was not cleared after a successful restart")
	}
}

// TestRecycleWatcher_Tick_RestartsIdlePendingFreelance mirrors the worker case
// above for a freelance-kind role (design.md D7: both kinds widened
// identically).
func TestRecycleWatcher_Tick_RestartsIdlePendingFreelance(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindFreelance, "myorch", "fl1", "/wt/fl1", "argus/fl1-branch", "mission")
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: true}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, true)
	testutil.Equal(t, runner.restartTaskID, task.ID)
}

// TestRecycleWatcher_Tick_MultiBindingTaskFindsCoordinatorBinding pins a fix
// for a task that legitimately holds 2+ live bindings (joined more than one
// orchestrator — e.g. a nested sub-coordinator bound as coordinator of its
// own team while also live under its parent's as a plain worker). The
// single-binding HeraLiveBindingByTask errors ErrHeraAmbiguous for such a
// task; tickTask must instead scan ListHeraLiveBindingsByTask for the
// coordinator-kind one so a real pending-recycle request doesn't get stuck
// retrying forever.
func TestRecycleWatcher_Tick_MultiBindingTaskFindsCoordinatorBinding(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "coord-orch", "/wt/coord", "argus/coord-branch", "mission")

	// The SAME task also joins a second orchestrator as a plain worker —
	// giving it 2 live bindings, which HeraLiveBindingByTask cannot resolve.
	otherOrch, err := d.CreateHeraOrchestrator("other-orch", "master")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: otherOrch.ID,
		Name:           "w-in-other-orch",
		Kind:           db.HeraKindWorker,
		ArgusProject:   task.Project,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)

	// Sanity: the task really is ambiguous under the single-binding lookup.
	_, err = d.HeraLiveBindingByTask(task.ID)
	if err == nil {
		t.Fatal("expected HeraLiveBindingByTask to be ambiguous for a task with 2 live bindings")
	}

	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"))

	runner := &fakeRecycleRunner{idle: true}
	w := NewRecycleWatcher(d, runner)
	w.Tick()

	testutil.Equal(t, runner.restartCalled, true)
	testutil.Equal(t, runner.restartTaskID, task.ID)
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
