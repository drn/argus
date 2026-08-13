package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeReclaimRunner reports a live session for a fixed taskID (HasSession)
// and records whether/how Stop was called — WITHOUT spawning a real process
// — so heraReclaimAndArchiveTask's marker-arming logic can be exercised
// deterministically, mirroring slowStopRunner in heraactions_cascade_race_test.go.
type fakeReclaimRunner struct {
	*agent.Runner
	liveTaskID string
	stopErr    error
	stopped    chan struct{}
}

func newFakeReclaimRunner(liveTaskID string) *fakeReclaimRunner {
	return &fakeReclaimRunner{Runner: agent.NewRunner(nil), liveTaskID: liveTaskID, stopped: make(chan struct{}, 1)}
}

func (r *fakeReclaimRunner) HasSession(taskID string) bool { return taskID == r.liveTaskID }

func (r *fakeReclaimRunner) Stop(taskID string) error {
	select {
	case r.stopped <- struct{}{}:
	default:
	}
	return r.stopErr
}

func (r *fakeReclaimRunner) waitStopped(t *testing.T) {
	t.Helper()
	select {
	case <-r.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop was never called")
	}
}

// TestHeraReclaimAndArchiveTask_LiveTaskCompletesAfterAsyncExit is the
// fix-nuke-completion-race regression: a task actively `in_progress` at the
// moment of reclaim must still reach `complete` once its reclaim-triggered
// stop's exit is observed later — not get permanently stranded at
// `in_review` by the ordinary crash/stop/fast-fail rule in
// handleSessionExitUI, which has no way to distinguish a deliberate,
// terminal nuke stop from an ordinary recoverable one.
func TestHeraReclaimAndArchiveTask_LiveTaskCompletesAfterAsyncExit(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	runner := newFakeReclaimRunner("tw")
	app := New(d, runner, false)

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	reclaimed := app.heraReclaimAndArchiveTask("tw")
	testutil.Equal(t, reclaimed, false) // no worktree/branch on this task, nothing to reclaim

	runner.waitStopped(t)

	// The archive-time snapshot must NOT force completion synchronously —
	// that's the existing, correct behavior from fix-hera-archive-status.
	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusInProgress)

	app.mu.Lock()
	armed := app.pendingHeraReclaim["tw"]
	app.mu.Unlock()
	testutil.Equal(t, armed, true) // marker survives the successful Stop() call

	// Simulate the async exit notification arriving later — a nuke's own
	// stop is never a "clean" exit (it's a signal), so cleanExit=false here,
	// exactly like an ordinary recoverable stop/crash would report.
	app.handleSessionExitUI("tw", false /* cleanExit */, false /* pendingRestart */)

	task, err = d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Status, model.StatusComplete) // NOT in_review — the regression this fixes

	app.mu.Lock()
	stillArmed := app.pendingHeraReclaim["tw"]
	app.mu.Unlock()
	testutil.Equal(t, stillArmed, false) // consumed — cannot leak into a later, unrelated exit
}

// TestHeraReclaimAndArchiveTask_NoLiveSessionMarkerDoesNotLeak covers the
// cleanup path: a task snapshotted in_progress but with no live runner
// session at reclaim time (HasSession false) never gets a Stop() call, so no
// exit notification is ever coming to consume the marker — it must be
// cleared immediately rather than left to leak into a later, unrelated exit
// of the same task ID.
func TestHeraReclaimAndArchiveTask_NoLiveSessionMarkerDoesNotLeak(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false) // no session registered for "tw"

	testutil.NoError(t, d.Add(&model.Task{ID: "tw", Name: "tw", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	app.heraReclaimAndArchiveTask("tw")

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
	testutil.Equal(t, task.Status, model.StatusInProgress) // untouched, matches existing behavior

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		armed := app.pendingHeraReclaim["tw"]
		app.mu.Unlock()
		if !armed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("pendingHeraReclaim marker leaked with no live session to ever consume it")
}

// TestHandleSessionExitUI_ReclaimMarker_WorkerBindingStillWins pins the PR
// #707 / BUG-050 invariant as defense-in-depth: even with the reclaim marker
// set, a task that still holds a live worker-kind hera binding at exit time
// rolls to in_review via RollHeraWorkerToReview, never force-completed. In
// the real nuke path the binding is always ended before the stop fires, so
// this is not reachable in production — but the ordering must not silently
// invert if that assumption is ever violated.
func TestHandleSessionExitUI_ReclaimMarker_WorkerBindingStillWins(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw") // adds task "tw" InProgress + a LIVE binding

	app.mu.Lock()
	app.pendingHeraReclaim["tw"] = true
	app.mu.Unlock()

	app.handleSessionExitUI("tw", false /* cleanExit */, false /* pendingRestart */)

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Status, model.StatusInReview) // invariant wins, not forced to complete

	app.mu.Lock()
	stillArmed := app.pendingHeraReclaim["tw"]
	app.mu.Unlock()
	testutil.Equal(t, stillArmed, false) // still consumed even though the roll branch fired
}
