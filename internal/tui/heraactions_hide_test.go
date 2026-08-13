package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
)

// fakeHideRunner reports a live session for a fixed taskID (HasSession) and
// records whether Stop was called, mirroring fakeReclaimRunner
// (heraactions_reclaim_race_test.go) – without spawning a real process – so
// heraHide's session-stop-on-hide-only behavior (add-hera-accept-lifecycle)
// can be exercised deterministically.
type fakeHideRunner struct {
	*agent.Runner
	liveTaskID string
	stopped    chan string
}

func newFakeHideRunner(liveTaskID string) *fakeHideRunner {
	return &fakeHideRunner{Runner: agent.NewRunner(nil), liveTaskID: liveTaskID, stopped: make(chan string, 1)}
}

func (r *fakeHideRunner) HasSession(taskID string) bool { return taskID == r.liveTaskID }

func (r *fakeHideRunner) Stop(taskID string) error {
	select {
	case r.stopped <- taskID:
	default:
	}
	return nil
}

func (r *fakeHideRunner) waitStopped(t *testing.T) string {
	t.Helper()
	select {
	case taskID := <-r.stopped:
		return taskID
	case <-time.After(2 * time.Second):
		t.Fatal("Stop was never called")
		return ""
	}
}

func (r *fakeHideRunner) neverStopped(t *testing.T) {
	t.Helper()
	select {
	case taskID := <-r.stopped:
		t.Fatalf("Stop was called unexpectedly for task %q", taskID)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestHeraHide_StopsLiveSessionOnHideDirection pins the add-hera-accept-
// lifecycle extension to heraHide: hiding a worker with a live session stops
// it (backgrounded), leaving the argus task row and worktree untouched.
func TestHeraHide_StopsLiveSessionOnHideDirection(t *testing.T) {
	d := testDB(t)
	runner := newFakeHideRunner("tw")
	app := New(d, runner, false)
	app.heraOps = hera.NewOps(d)

	orch := seedHeraOrch(t, d, "o")
	role := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	sel := hera.Selection{Role: &hera.RoleView{RoleID: role.ID, OrchID: orch, Name: "w", Kind: db.HeraKindWorker, TaskID: "tw", Live: true}}

	app.heraHide(sel)

	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt != nil, true) // hidden

	stoppedTaskID := runner.waitStopped(t)
	testutil.Equal(t, stoppedTaskID, "tw")

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, false) // HIDE never touches the argus task row
}

// TestHeraHide_UnhideNeverStopsSession pins the direction gate: pressing `a`
// on an already-hidden role (un-hide) never stops any session, even though
// the fake runner reports one live.
func TestHeraHide_UnhideNeverStopsSession(t *testing.T) {
	d := testDB(t)
	runner := newFakeHideRunner("tw")
	app := New(d, runner, false)
	app.heraOps = hera.NewOps(d)

	orch := seedHeraOrch(t, d, "o")
	role := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.ArchiveHeraRole(role.ID)) // pre-hidden

	sel := hera.Selection{Role: &hera.RoleView{RoleID: role.ID, OrchID: orch, Name: "w", Kind: db.HeraKindWorker, TaskID: "tw", Live: true, Archived: true}}
	app.heraHide(sel)

	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt == nil, true) // restored (unhidden)

	runner.neverStopped(t)
}

// TestHeraHide_NoLiveSessionIsCleanNoOpOnStopPath covers hiding a worker with
// no live session: the archive still succeeds and no stop attempt is made
// (there is nothing to stop).
func TestHeraHide_NoLiveSessionIsCleanNoOpOnStopPath(t *testing.T) {
	d := testDB(t)
	runner := newFakeHideRunner("some-other-task")
	app := New(d, runner, false)
	app.heraOps = hera.NewOps(d)

	orch := seedHeraOrch(t, d, "o")
	role := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	sel := hera.Selection{Role: &hera.RoleView{RoleID: role.ID, OrchID: orch, Name: "w", Kind: db.HeraKindWorker, TaskID: "tw", Live: true}}

	app.heraHide(sel)

	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt != nil, true) // hidden regardless

	runner.neverStopped(t)
}
