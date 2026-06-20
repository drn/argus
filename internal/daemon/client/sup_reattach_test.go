package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// These tests prove the P3 payoff end-to-end with the REAL stack: a daemon
// bounce does NOT interrupt a live agent. A single long-lived daemon.Supervisor
// owns the agent across two successive daemon-as-client incarnations (the bounce
// = a NEW *Client + daemon.Daemon against the SAME live supervisor). They never
// touch the real daemon/supervisor sockets — t.TempDir() + t.Setenv("HOME").
//
// Short test names: t.TempDir() embeds the name in the Unix socket path and
// macOS caps sun_path at 104 bytes.

// reattachHarness spins one long-lived Supervisor over a temp socket and returns
// it plus the shared DB. The supervisor outlives daemon bounces (its Shutdown is
// the only cleanup), exactly as in production.
func reattachHarness(t *testing.T) (supSock string, database *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	supSock = filepath.Join(t.TempDir(), "s.sock")
	sup := daemon.NewSupervisor(database.Config)
	go sup.Serve(supSock) //nolint:errcheck
	t.Cleanup(func() { sup.Shutdown() })
	waitFile(t, supSock)
	return supSock, database
}

// connectDaemon builds a fresh daemon-as-client incarnation against the live
// supervisor: a new *Client + a daemon.Daemon that mounts it (UseSupervisorRunner).
func connectDaemon(t *testing.T, supSock string, database *db.DB) (*daemon.Daemon, *Client) {
	t.Helper()
	sc, err := Connect(supSock)
	testutil.NoError(t, err)
	d := daemon.New(database)
	d.UseSupervisorRunner(sc)
	return d, sc
}

// startLive adds an InProgress task backed by cmd and starts it through the
// supervisor-client. Returns the task.
func startLive(t *testing.T, sc *Client, database *db.DB, id, cmd, worktree string) *model.Task {
	t.Helper()
	bk := "be-" + id
	testutil.NoError(t, database.SetBackend(bk, config.Backend{Command: cmd}))
	task := &model.Task{ID: id, Name: id, Status: model.StatusInProgress, Backend: bk, Worktree: worktree}
	testutil.NoError(t, database.Add(task))
	_, err := sc.Start(task, config.Config{}, 24, 80, false)
	testutil.NoError(t, err)
	return task
}

func waitAliveInSup(t *testing.T, sc *Client, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range sc.ListSessionInfo() {
			if s.TaskID == taskID && s.Alive {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s never went alive in the supervisor", taskID)
}

// bindHeraWorker creates an orchestrator + worker role + live binding for taskID.
func bindHeraWorker(t *testing.T, database *db.DB, taskID string) int64 {
	t.Helper()
	o, err := database.CreateHeraOrchestrator("o-"+taskID, "")
	testutil.NoError(t, err)
	r, err := database.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "proj"})
	testutil.NoError(t, err)
	b, err := database.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
	return b.ID
}

// TestSupReattach is the headline P3 test: a daemon bounce leaves a live agent
// running. We start a long-lived session under the supervisor through daemon #1,
// bounce the daemon (Close #1 — the supervisor-mode cleanup path — and build #2
// against the SAME live supervisor), then run #2's startup reconcile. Asserts:
// (a) the session is still alive in the supervisor, (b) #2 re-attaches and
// ListSessions reports it, (c) the task stays InProgress (NOT flipped), and
// (d) a hera worker binding on the task stays live.
func TestSupReattach(t *testing.T) {
	supSock, database := reattachHarness(t)

	// --- daemon #1: start a long-lived agent ---
	_, sc1 := connectDaemon(t, supSock, database)
	task := startLive(t, sc1, database, "reat", "sh -c 'sleep 30'", t.TempDir())
	bindingID := bindHeraWorker(t, database, task.ID)
	waitAliveInSup(t, sc1, task.ID)

	// --- bounce: detach #1 (supervisor-mode cleanup = Close, never StopAll, so
	// the agent keeps running), then bring up #2 against the SAME supervisor ---
	testutil.NoError(t, sc1.Close())
	d2, sc2 := connectDaemon(t, supSock, database)
	t.Cleanup(func() { sc2.Close() }) //nolint:errcheck

	d2.ReconcileOnStartup()

	// (a) the agent is still alive in the supervisor across the bounce.
	waitAliveInSup(t, sc2, task.ID)

	// (b) #2 re-attached — Get resolves the live session through the supervisor.
	if sc2.Get(task.ID) == nil {
		t.Fatal("daemon #2 failed to re-attach the live session")
	}

	// (c) the task stayed InProgress — re-attached, never flipped to InReview.
	got, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInProgress)

	// (d) the hera binding survived the bounce (keyed on task-row existence).
	bindings, err := database.ListHeraLiveBindingsByTask(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(bindings), 1)
	testutil.Equal(t, bindings[0].ID, bindingID)
}

// TestSupReattachComplete proves #707 holds ACROSS the bounce: a re-attached
// session that later exits cleanly still flips the task to Complete. This works
// only because the startup re-attach (Get) armed daemon #2's exit relay — without
// it, the clean exit would never reach handleSessionExit. The agent blocks on a
// sentinel file so it is provably still alive at re-attach, then exits 0 when we
// create the file. (Non-hera task, so the clean exit lands Complete, not the
// worker InReview policy.)
func TestSupReattachComplete(t *testing.T) {
	supSock, database := reattachHarness(t)

	wt := t.TempDir()
	sentinel := filepath.Join(wt, "DONE")
	cmd := "sh -c 'while [ ! -f " + sentinel + " ]; do sleep 0.05; done'"

	_, sc1 := connectDaemon(t, supSock, database)
	task := startLive(t, sc1, database, "707b", cmd, wt)
	waitAliveInSup(t, sc1, task.ID)

	// Bounce: detach #1, bring up #2, re-attach (arms #2's exit relay).
	testutil.NoError(t, sc1.Close())
	d2, sc2 := connectDaemon(t, supSock, database)
	t.Cleanup(func() { sc2.Close() }) //nolint:errcheck
	d2.ReconcileOnStartup()
	waitAliveInSup(t, sc2, task.ID)

	// Let the agent exit cleanly (exit 0). The armed relay delivers the clean
	// ExitInfo to #2's DB → Complete (#707 across the bounce).
	testutil.NoError(t, os.WriteFile(sentinel, []byte("x"), 0o644))
	waitStatus(t, database, task.ID, model.StatusComplete)
}
