package daemon

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- Daemon.ForceRecycleCoordinator (fix-coordhook-idle-deadlock, Part B) ---
//
// This RPC verb is `argus coord-hook`'s hard-stop escalation trigger: once a
// coordinator's context_size crosses 1.5x its configured budget, the hook
// calls this over the daemon's Unix socket to force an immediate
// kill-and-restart, bypassing the idle gate self-service recycle_coord waits
// for. It mirrors internal/tui/heraactions.go's heraDoForceRecycle (the
// rail's `B` key) exactly, just reachable from outside the TUI process.

// TestForceRecycleCoordinator_HappyPath pins the successful path: a task
// bound to a coordinator role, with no live session yet, is force-recycled
// (Restart's "no live session" branch starts a fresh one directly).
func TestForceRecycleCoordinator_HappyPath(t *testing.T) {
	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: "echo hello"}))
	task, _ := seedHeraRecycleCoordinator(t, d.db, t.TempDir(), "You are the coordinator.")

	var resp StatusResp
	testutil.NoError(t, rpcFor(d).ForceRecycleCoordinator(&TaskIDReq{TaskID: task.ID}, &resp))
	testutil.Equal(t, resp.Error, "")
	testutil.Equal(t, resp.OK, true)

	updated, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Contains(t, updated.Prompt, "You are the coordinator.")
}

// TestForceRecycleCoordinator_UnknownTask_Errors covers a task ID with no
// hera binding at all.
func TestForceRecycleCoordinator_UnknownTask_Errors(t *testing.T) {
	d, _ := testDaemon(t)

	var resp StatusResp
	testutil.NoError(t, rpcFor(d).ForceRecycleCoordinator(&TaskIDReq{TaskID: "no-such-task"}, &resp))
	testutil.Equal(t, resp.OK, false)
	if resp.Error == "" {
		t.Fatal("expected a non-empty error for an unbound task")
	}
}

// TestForceRecycleCoordinator_WorkerOnly_Errors covers a task bound only as a
// worker (no coordinator-kind role bound) — ForceRecycleCoordinator must
// reject rather than recycle the wrong kind of role.
func TestForceRecycleCoordinator_WorkerOnly_Errors(t *testing.T) {
	d, _ := testDaemon(t)

	task := &model.Task{ID: "worker-task", Name: "worker-task", Status: model.StatusInProgress, Project: "test-project"}
	testutil.NoError(t, d.db.Add(task))
	orch, err := d.db.CreateHeraOrchestrator("myorch", "master")
	testutil.NoError(t, err)
	_, _, err = d.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "worker-1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   task.Project,
		Prompt:         "do the thing",
	}, task.ID, "")
	testutil.NoError(t, err)

	var resp StatusResp
	testutil.NoError(t, rpcFor(d).ForceRecycleCoordinator(&TaskIDReq{TaskID: task.ID}, &resp))
	testutil.Equal(t, resp.OK, false)
	if resp.Error == "" {
		t.Fatal("expected a non-empty error for a task with only a worker-kind binding")
	}
}
