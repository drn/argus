package hera

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_revive (add-hera-revive) ---
//
// fakeReviveRunner records calls so tests can assert gating/ordering without a
// real PTY/session, mirroring fakeRecycleRunner's shape (recycle_test.go).
type fakeReviveRunner struct {
	alive         bool
	idle          bool
	blocked       bool
	pending       bool
	restartErr    error
	kickErr       error
	restartCalled bool
	kickCalled    bool
	restartTaskID string
	kickTaskID    string
}

func (f *fakeReviveRunner) IsAlive(taskID string) bool           { return f.alive }
func (f *fakeReviveRunner) IsIdle(taskID string) bool            { return f.idle }
func (f *fakeReviveRunner) BlockedOnPrompt(taskID string) bool   { return f.blocked }
func (f *fakeReviveRunner) HasPendingRestart(taskID string) bool { return f.pending }

func (f *fakeReviveRunner) KickRerender(taskID string) error {
	f.kickCalled = true
	f.kickTaskID = taskID
	return f.kickErr
}

func (f *fakeReviveRunner) RestartDead(taskID string) error {
	f.restartCalled = true
	f.restartTaskID = taskID
	return f.restartErr
}

func TestReviveRole_TaskNotFound(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, err = ReviveRole(d, &fakeReviveRunner{}, "no-such-task", false)
	if err == nil {
		t.Fatal("expected an error for a missing task")
	}
}

func TestReviveRole_DeadSessionRestarted(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")

	runner := &fakeReviveRunner{alive: false}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveRestartedDead)
	testutil.Equal(t, runner.restartCalled, true)
	testutil.Equal(t, runner.restartTaskID, task.ID)
	testutil.Equal(t, runner.kickCalled, false)
}

func TestReviveRole_DeadSessionRestartFails(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")

	runner := &fakeReviveRunner{alive: false, restartErr: errors.New("boom")}
	_, err = ReviveRole(d, runner, task.ID, false)
	if err == nil {
		t.Fatal("expected the restart failure to propagate")
	}
}

// deadCoordinator (any role kind) is restarted too — the coordinator-live
// gate below only applies to a LIVE session.
func TestReviveRole_DeadCoordinatorRestarted(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "orch", "/wt/coord", "argus/coord", "mission")

	runner := &fakeReviveRunner{alive: false}
	outcome, err := ReviveRole(d, runner, task.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveRestartedDead)
	testutil.Equal(t, runner.restartCalled, true)
}

func TestReviveRole_LiveCoordinatorNeverRevived(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleCoordinator(t, d, "orch", "/wt/coord", "argus/coord", "mission")
	testutil.NoError(t, d.Update(withSessionID(task, "sess-1")))

	runner := &fakeReviveRunner{alive: true, idle: true}
	outcome, err := ReviveRole(d, runner, task.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveSkippedCoordinatorLive)
	testutil.Equal(t, runner.kickCalled, false)
	testutil.Equal(t, runner.restartCalled, false)
}

func TestReviveRole_NoSessionID(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	// task.SessionID is empty by default from seedRecycleRole.

	runner := &fakeReviveRunner{alive: true, idle: true}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveSkippedNoSessionID)
	testutil.Equal(t, runner.kickCalled, false)
}

func TestReviveRole_RestartPending(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	testutil.NoError(t, d.Update(withSessionID(task, "sess-1")))

	runner := &fakeReviveRunner{alive: true, idle: true, pending: true}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveSkippedPending)
	testutil.Equal(t, runner.kickCalled, false)
}

func TestReviveRole_BusySkipped(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	testutil.NoError(t, d.Update(withSessionID(task, "sess-1")))

	runner := &fakeReviveRunner{alive: true, idle: false}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveSkippedBusy)
	testutil.Equal(t, runner.kickCalled, false)
}

func TestReviveRole_BlockedOnPromptSkipped(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	testutil.NoError(t, d.Update(withSessionID(task, "sess-1")))

	runner := &fakeReviveRunner{alive: true, idle: true, blocked: true}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveSkippedBlocked)
	testutil.Equal(t, runner.kickCalled, false)
}

func TestReviveRole_KickedStuckRestoresInProgress(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	task = withSessionID(task, "sess-1")
	task.Status = model.StatusInReview
	testutil.NoError(t, d.Update(task))

	runner := &fakeReviveRunner{alive: true, idle: true, blocked: false}
	outcome, err := ReviveRole(d, runner, task.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, outcome, ReviveKickedStuck)
	testutil.Equal(t, runner.kickCalled, true)
	testutil.Equal(t, runner.kickTaskID, task.ID)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInProgress)
}

func TestReviveRole_KickFails(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, _ := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	testutil.NoError(t, d.Update(withSessionID(task, "sess-1")))

	runner := &fakeReviveRunner{alive: true, idle: true, kickErr: errors.New("boom")}
	_, err = ReviveRole(d, runner, task.ID, false)
	if err == nil {
		t.Fatal("expected the kick failure to propagate")
	}
}

// withSessionID returns task with SessionID set — a small helper since
// seedRecycleRole (recycle_test.go) does not set one, and the "alive" gate
// checks below need a non-empty SessionID to reach the idle/blocked checks.
func withSessionID(task *model.Task, sessionID string) *model.Task {
	task.SessionID = sessionID
	return task
}
