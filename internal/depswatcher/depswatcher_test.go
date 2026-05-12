package depswatcher

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// stubSession is the no-op SessionHandle the fake provider returns from Start.
// All methods are zero-effect because the watcher only inspects PID(); the
// other methods exist to satisfy the interface.
type stubSession struct{ pid int }

func (s *stubSession) PID() int                                         { return s.pid }
func (s *stubSession) WriteInput(_ []byte) (int, error)                 { return 0, nil }
func (s *stubSession) Resize(_, _ uint16) error                         { return nil }
func (s *stubSession) RecentOutput() []byte                             { return nil }
func (s *stubSession) RecentOutputTail(_ int) []byte                    { return nil }
func (s *stubSession) RecentOutputTailWithTotal(_ int) ([]byte, uint64) { return nil, 0 }
func (s *stubSession) TotalWritten() uint64                             { return 0 }
func (s *stubSession) IsIdle() bool                                     { return false }
func (s *stubSession) LastInput() time.Time                             { return time.Time{} }
func (s *stubSession) Alive() bool                                      { return true }
func (s *stubSession) PTYSize() (int, int)                              { return 80, 24 }
func (s *stubSession) InitialPTYSize() (int, int)                       { return 80, 24 }
func (s *stubSession) Done() <-chan struct{}                            { c := make(chan struct{}); close(c); return c }
func (s *stubSession) Err() error                                       { return nil }
func (s *stubSession) WorkDir() string                                  { return "" }
func (s *stubSession) Stop() error                                      { return nil }
func (s *stubSession) AddWriter(_ io.Writer)                            {}
func (s *stubSession) AddWriterFrom(_ io.Writer, _ uint64)              {}
func (s *stubSession) AddWriterFromTolerant(_ io.Writer, _ uint64)      {}
func (s *stubSession) RemoveWriter(_ io.Writer)                         {}

// fakeProvider is a SessionProvider that records Start calls. It never spawns
// a real process — agent.StartPendingBlocked happily threads the stub session
// through and the watcher's transition logic is exercised in isolation.
type fakeProvider struct {
	mu       sync.Mutex
	started  []string
	failNext bool
	// existing simulates sessions already in the runner map — used by
	// the double-start-guard test where a prior Start succeeded but the
	// DB write failed, leaving a live session that the next Start call
	// must NOT overwrite.
	existing map[string]agent.SessionHandle
}

// preInstall registers a fake session under taskID as if a prior Start
// had succeeded. HasSession/Get will return it; Start will panic if
// called with the same ID (the guard should short-circuit before).
func (f *fakeProvider) preInstall(taskID string, sess agent.SessionHandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.existing == nil {
		f.existing = make(map[string]agent.SessionHandle)
	}
	f.existing[taskID] = sess
}

func (f *fakeProvider) Start(task *model.Task, _ config.Config, _, _ uint16, _ bool) (agent.SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return nil, errFailStart
	}
	f.started = append(f.started, task.ID)
	return &stubSession{pid: 42}, nil
}
func (f *fakeProvider) Stop(_ string) error { return nil }
func (f *fakeProvider) StopAll()            {}
func (f *fakeProvider) Get(taskID string) agent.SessionHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existing[taskID]
}
func (f *fakeProvider) Running() []string                    { return nil }
func (f *fakeProvider) Idle() []string                       { return nil }
func (f *fakeProvider) RunningAndIdle() ([]string, []string) { return nil, nil }
func (f *fakeProvider) HasSession(taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.existing[taskID]
	return ok
}
func (f *fakeProvider) WorkDir(_ string) string         { return "" }
func (f *fakeProvider) HasPendingRestart(_ string) bool { return false }

func (f *fakeProvider) startedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.started))
	copy(out, f.started)
	return out
}

// errFailStart is the sentinel the fake provider returns when failNext is set.
// Plain error so callers can compare without errors.Is plumbing.
type fakeErr string

func (e fakeErr) Error() string { return string(e) }

var errFailStart fakeErr = "stub: start failed"

// TestWatcher_StartsWhenDepsComplete is the happy path: a blocked-pending
// task whose only dep is complete gets started on tick.
func TestWatcher_StartsWhenDepsComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "dep", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "child",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("expected child started; got %v", got)
	}

	fresh, err := d.Get(child.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, fresh.Status, model.StatusInProgress)
	if fresh.StartedAt.IsZero() {
		t.Fatalf("StartedAt not stamped")
	}
}

// TestWatcher_StaysBlockedWhenAnyDepPending covers the all-or-nothing rule:
// a single non-complete dep keeps the task pending. The complete dep is
// added to confirm we're checking the SET not just the first entry.
func TestWatcher_StaysBlockedWhenAnyDepPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep1 := mustAdd(t, d, &model.Task{Name: "d1", Status: model.StatusComplete})
	dep2 := mustAdd(t, d, &model.Task{Name: "d2", Status: model.StatusInProgress})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep1.ID, dep2.ID},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no starts; got %v", got)
	}

	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusPending)
}

// TestWatcher_MissingDepKeepsBlocked covers the broken-link case: a task
// references a dep ID that has been deleted. The watcher refuses to start
// the task — the orchestrator must observe and repair.
func TestWatcher_MissingDepKeepsBlocked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{"gone-id"},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no starts on missing dep; got %v", got)
	}
	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusPending)
}

// TestWatcher_NoDepsIgnored covers the legacy single-task path: a pending
// task with no depends_on should NOT be started by the watcher; that's
// CreateAndStart's responsibility. The watcher only resolves DAG state.
func TestWatcher_NoDepsIgnored(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	mustAdd(t, d, &model.Task{Name: "loose", Status: model.StatusPending})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no starts; got %v", got)
	}
}

// TestWatcher_ArchivedSkipped guards against starting an archived blocked
// task (the orchestrator archived it to abort the stack).
func TestWatcher_ArchivedSkipped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		Archived:  true,
		DependsOn: []string{dep.ID},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no starts for archived task; got %v", got)
	}
}

// TestWatcher_RetryOnStartFailure covers the recoverable-error path. First
// tick: provider returns an error, task stays pending. Second tick after
// flipping the flag: task starts. This is what lets the watcher self-heal
// after a transient failure like worktree-missing.
func TestWatcher_RetryOnStartFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	fp := &fakeProvider{failNext: true}
	w := New(d, fp)
	w.tick()

	// First tick failed: task still pending, no start recorded.
	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusPending)
	testutil.Equal(t, len(fp.startedIDs()), 0)

	w.tick()
	fresh2, _ := d.Get(child.ID)
	testutil.Equal(t, fresh2.Status, model.StatusInProgress)
	if got := fp.startedIDs(); len(got) != 1 {
		t.Fatalf("expected one start on retry; got %v", got)
	}
}

// TestWatcher_OnStartCallback covers the OnStart hook used by daemon.go to
// fire a push notification when a blocked task transitions live.
func TestWatcher_OnStartCallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	var got []string
	w.SetOnStart(func(t *model.Task) { got = append(got, t.ID) })
	w.tick()

	if len(got) != 1 || got[0] != child.ID {
		t.Fatalf("expected callback for child; got %v", got)
	}
}

// TestWatcher_UnblockSilentSkipOnStatusChange covers the read-then-decide
// race window: the scan sees a pending task, but by the time unblock's
// re-fetch lands the orchestrator has already advanced the row (e.g.
// archived to skip, or another path marked it complete). The watcher
// silently bows out instead of stomping on the newer state.
func TestWatcher_UnblockSilentSkipOnStatusChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	// Race the unblock path: hand a snapshot reading Pending to unblock,
	// but flip the DB row to InProgress beforehand. unblock's re-fetch
	// catches the state change and exits without calling Start.
	child.Status = model.StatusInProgress
	testutil.NoError(t, d.Update(child))

	fp := &fakeProvider{}
	w := New(d, fp)
	// Pass a stale view of the task that still claims Pending.
	stale := *child
	stale.Status = model.StatusPending
	w.unblock(&stale)

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no start on race; got %v", got)
	}
	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusInProgress)
}

// TestWatcher_UnblockSilentSkipOnArchive covers the archive race window:
// the scan saw Status=Pending and !Archived, but between the scan and the
// re-fetch the orchestrator archived the task to abort the stack. Without
// the Archived guard in unblock, the watcher would still start the agent
// because Archived is independent of Status — a row stays Pending after
// archive.
func TestWatcher_UnblockSilentSkipOnArchive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	// Race: archive the row before unblock's re-fetch.
	child.Archived = true
	testutil.NoError(t, d.Update(child))

	fp := &fakeProvider{}
	w := New(d, fp)
	// Stale snapshot still claims un-archived Pending.
	stale := *child
	stale.Archived = false
	w.unblock(&stale)

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no start on archive race; got %v", got)
	}
}

// TestWatcher_UnblockSilentSkipOnDeletedTask covers the reload-failure
// branch in unblock: a row that has been deleted between scan and unblock
// surfaces an error from db.Get; the watcher logs and skips.
func TestWatcher_UnblockSilentSkipOnDeletedTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	fp := &fakeProvider{}
	w := New(d, fp)

	// Synthetic task not in the DB. Mirrors the scan-then-delete race.
	stale := &model.Task{ID: "ghost", Status: model.StatusPending}
	w.unblock(stale)

	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected no start on missing row; got %v", got)
	}
}

// TestWatcher_FailedDepStillUnblocks documents the orchestrator contract
// per context/knowledge/gotchas/orchestration.md: the watcher gates on
// Status alone, not on result.failed. A dep that reached StatusComplete
// with `{"failed": true, ...}` is, from the daemon's perspective, "done";
// the orchestrator owns the decision to halt the stack. Without this test
// a well-intentioned future fix that filters by !failed would silently
// break stacked-PR remediation flows where the orchestrator is expected
// to read the failed flag and decide whether to task_stop downstream.
func TestWatcher_FailedDepStillUnblocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{
		Name:   "dep",
		Status: model.StatusComplete,
		Result: `{"failed":true,"reason":"build broke"}`,
	})
	child := mustAdd(t, d, &model.Task{
		Name:      "child",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	fp := &fakeProvider{}
	w := New(d, fp)
	w.tick()

	if got := fp.startedIDs(); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("expected child to start despite dep.result.failed=true; got %v", got)
	}
	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusInProgress)
}

// TestWatcher_StopHaltsLoop drives the actual goroutine to confirm Stop
// unblocks Start. Without the close-on-stopCh contract a missed Stop would
// hang the daemon shutdown.
func TestWatcher_StopHaltsLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)

	fp := &fakeProvider{}
	w := New(d, fp)
	w.SetInterval(5 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		w.Start()
		close(done)
	}()

	// Let at least one tick run, then stop.
	time.Sleep(20 * time.Millisecond)
	w.Stop()
	// Double Stop must be safe — channel close guard.
	w.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not exit after Stop")
	}
}

// --- helpers ---

func mustOpenDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func mustAdd(t *testing.T, d *db.DB, task *model.Task) *model.Task {
	t.Helper()
	if err := d.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return task
}

// TestWatcher_DoubleStartGuardOnDBUpdateFailure simulates the rare race
// where runner.Start succeeds but database.Update fails inside
// StartPendingBlocked, leaving the task in Pending status with a live
// session in the runner. On the next watcher tick, StartPendingBlocked
// must see the existing session via HasSession and sync the DB rather
// than spawning a second process. Without the guard, runner.Start
// would overwrite the session map slot and orphan the original process.
func TestWatcher_DoubleStartGuardOnDBUpdateFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := mustOpenDB(t)
	dep := mustAdd(t, d, &model.Task{Name: "d", Status: model.StatusComplete})
	child := mustAdd(t, d, &model.Task{
		Name:      "c",
		Status:    model.StatusPending,
		DependsOn: []string{dep.ID},
	})

	// Pre-register a fake "existing session" in the provider so the next
	// StartPendingBlocked call sees HasSession=true and takes the sync path.
	fp := &fakeProvider{}
	fp.preInstall(child.ID, &stubSession{pid: 4242})

	w := New(d, fp)
	w.tick()

	// Provider's started slice must be EMPTY: the guard short-circuited
	// before runner.Start was called.
	if got := fp.startedIDs(); len(got) != 0 {
		t.Fatalf("expected runner.Start NOT to be called when session exists; got %v", got)
	}
	fresh, _ := d.Get(child.ID)
	testutil.Equal(t, fresh.Status, model.StatusInProgress)
	testutil.Equal(t, fresh.AgentPID, 4242)
}
