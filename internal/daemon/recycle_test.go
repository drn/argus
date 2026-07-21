package daemon

import (
	"time"

	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// recycleTestConfig returns a config whose "test" backend is a
// non-Claude, long-running command so StopStrayJobs is a fast, deterministic
// no-op (real `claude agents` cleanup is only meaningful for Claude
// backends — see internal/agent/stray.go) while the session itself stays
// alive long enough to observe kill/restart.
func recycleTestConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sh -c 'while :; do sleep 1; done'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
}

// seedHeraRecycleCoordinator creates a task + orchestrator + bound
// coordinator role for heraRecycleRunner tests, mirroring
// internal/hera/recycle_test.go's seedRecycleCoordinator (unexported to that
// package, so re-implemented here against the same DB surface).
func seedHeraRecycleCoordinator(t *testing.T, database *db.DB, worktree, mission string) (*model.Task, *db.HeraRole) {
	t.Helper()
	task := &model.Task{
		ID:       "coord-task",
		Name:     "coord-task",
		Status:   model.StatusInProgress,
		Project:  "test-project",
		Worktree: worktree,
		Backend:  "test",
	}
	testutil.NoError(t, database.Add(task))

	orch, err := database.CreateHeraOrchestrator("myorch", "master")
	testutil.NoError(t, err)

	role, _, err := database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.HeraKindCoordinator,
		ArgusProject:   task.Project,
		Prompt:         mission,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)

	return task, role
}

func TestHeraRecycleRunner_IsIdle_NoSessionIsIdle(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	testutil.Equal(t, r.IsIdle("no-such-task"), true)
}

func TestHeraRecycleRunner_StopStrayJobs_NoopForNonClaudeBackend(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, _ := seedHeraRecycleCoordinator(t, database, t.TempDir(), "mission")

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	testutil.NoError(t, r.StopStrayJobs(task.ID, "sess-1"))
}

func TestHeraRecycleRunner_StopStrayJobs_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	err = r.StopStrayJobs("no-such-task", "sess-1")
	if err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

// TestHeraRecycleRunner_Restart_EndToEnd pins design.md D5's "same task
// survives" + "no stale SessionID pinned" + "seed prompt assembled" acceptance
// criteria against the REAL runner: Restart must clear the outgoing
// SessionID, persist the assembled seed prompt as the fresh task's Prompt,
// and hand off to runner.Recycle so the exit goroutine resurrects a genuinely
// new session on the identical task row.
func TestHeraRecycleRunner_Restart_EndToEnd(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, role := seedHeraRecycleCoordinator(t, database, t.TempDir(), "You are the coordinator.")
	task.SessionID = "stale-sid-1"
	testutil.NoError(t, database.Update(task))

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()

	sess1, err := runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(runner.StopAll)

	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })
	testutil.NoError(t, r.Restart(task.ID, role.ID))

	// Persisted row must have its stale SessionID cleared and a non-empty
	// seed prompt (mission text) set before the restart landed.
	updated, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, updated.SessionID, "")
	testutil.Contains(t, updated.Prompt, "You are the coordinator.")

	// Wait for the old session to die and the fresh one to land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runner.HasPendingRestart(task.ID) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		newSess := runner.Get(task.ID)
		if newSess != nil && newSess != sess1 && newSess.Alive() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for Restart to resurrect the session")
}

// TestHeraRecycleRunner_Restart_NoLiveSession_StartsFresh pins the fix for a
// coordinator whose session already exited (e.g. crashed) between requesting
// a self-service recycle and the watcher's next tick: IsIdle treats a missing
// session as "proceed" (see IsIdle's doc comment), so Restart must actually
// succeed in that case by starting a fresh session directly rather than
// routing through runner.Recycle (which requires a live session to stop and
// would return ErrSessionNotFound, permanently stranding the pending-recycle
// intent).
func TestHeraRecycleRunner_Restart_NoLiveSession_StartsFresh(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, role := seedHeraRecycleCoordinator(t, database, t.TempDir(), "You are the coordinator.")

	runner := agent.NewRunner(nil) // no session ever started for this task
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	testutil.NoError(t, r.Restart(task.ID, role.ID))
	t.Cleanup(runner.StopAll)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := runner.Get(task.ID); sess != nil && sess.Alive() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for Restart to start a fresh session when none existed")
}

// TestHeraRecycleRunner_Restart_DualLiveBindingResolvesViaRoleID pins the fix
// for a task that legitimately holds 2+ live hera bindings — e.g. bound as
// coordinator of its own orchestrator while also joined as a plain worker
// under a second one. Before the fix, Restart re-derived the binding from
// taskID alone via the single-binding HeraLiveBindingByTask, which returns
// ErrHeraAmbiguous for such a task even though the caller (RecycleCoord, via
// the already-resolved roleID) had no ambiguity at all. Restart must resolve
// via HeraLiveBindingByRole(roleID) instead, so the already-disambiguated
// role is never re-derived ambiguously.
func TestHeraRecycleRunner_Restart_DualLiveBindingResolvesViaRoleID(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, role := seedHeraRecycleCoordinator(t, database, t.TempDir(), "You are the coordinator.")

	// The SAME task also joins a second orchestrator as a plain worker,
	// giving it 2 live bindings.
	otherOrch, err := database.CreateHeraOrchestrator("other-orch", "master")
	testutil.NoError(t, err)
	_, _, err = database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: otherOrch.ID,
		Name:           "w-in-other-orch",
		Kind:           db.HeraKindWorker,
		ArgusProject:   task.Project,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)

	// Sanity: the task really is ambiguous under the single-binding lookup —
	// this is exactly what tripped up the pre-fix Restart.
	_, err = database.HeraLiveBindingByTask(task.ID)
	if err == nil {
		t.Fatal("expected HeraLiveBindingByTask to be ambiguous for a task with 2 live bindings")
	}

	runner := agent.NewRunner(nil) // no session ever started for this task
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	testutil.NoError(t, r.Restart(task.ID, role.ID))
	t.Cleanup(runner.StopAll)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := runner.Get(task.ID); sess != nil && sess.Alive() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for Restart to start a fresh session for a dual-bound task")
}

func TestHeraRecycleRunner_Restart_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraRecycleRunner(database, runner, func() config.Config { return cfg })

	err = r.Restart("no-such-task", 0)
	if err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}
