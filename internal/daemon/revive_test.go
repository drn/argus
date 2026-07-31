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

// seedHeraReviveWorker creates a task + orchestrator + bound worker role for
// HeraReviveRunner tests, mirroring seedHeraRecycleCoordinator (recycle_test.go)
// against the same DB surface.
func seedHeraReviveWorker(t *testing.T, database *db.DB, worktree, mission string) (*model.Task, *db.HeraRole) {
	t.Helper()
	task := &model.Task{
		ID:       "worker-task",
		Name:     "worker-task",
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
		Name:           "worker-1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   task.Project,
		Prompt:         mission,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)

	return task, role
}

func TestHeraReviveRunner_IsAlive_NoSessionIsFalse(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	testutil.Equal(t, r.IsAlive("no-such-task"), false)
}

func TestHeraReviveRunner_IsIdle_NoSessionIsFalse(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	testutil.Equal(t, r.IsIdle("no-such-task"), false)
}

func TestHeraReviveRunner_BlockedOnPrompt_NoSessionIsFalse(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	testutil.Equal(t, r.BlockedOnPrompt("no-such-task"), false)
}

func TestHeraReviveRunner_HasPendingRestart_NoneIsFalse(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	testutil.Equal(t, r.HasPendingRestart("no-such-task"), false)
}

func TestHeraReviveRunner_KickRerender_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	if err := r.KickRerender("no-such-task"); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

func TestHeraReviveRunner_KickRerender_NoLiveSessionErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, _ := seedHeraReviveWorker(t, database, t.TempDir(), "mission")

	runner := agent.NewRunner(nil) // no session ever started
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	if err := r.KickRerender(task.ID); err == nil {
		t.Fatal("expected an error for a task with no live session")
	}
}

// TestHeraReviveRunner_KickRerender_PreservesPTYSize_EndToEnd pins design.md
// D3's stated divergence from the TUI's Enter-key kick: a headless caller has
// no pane to fit, so KickRerender must preserve the session's EXISTING PTY
// size rather than resizing it.
func TestHeraReviveRunner_KickRerender_PreservesPTYSize_EndToEnd(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, _ := seedHeraReviveWorker(t, database, t.TempDir(), "mission")

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()

	sess1, err := runner.Start(task, cfg, 31, 97, false)
	testutil.NoError(t, err)
	t.Cleanup(runner.StopAll)

	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })
	testutil.NoError(t, r.KickRerender(task.ID))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runner.HasPendingRestart(task.ID) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		newSess := runner.Get(task.ID)
		if newSess != nil && newSess != sess1 && newSess.Alive() {
			cols, rows := newSess.PTYSize()
			testutil.Equal(t, cols, 97)
			testutil.Equal(t, rows, 31)
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for KickRerender to resurrect the session")
}

func TestHeraReviveRunner_RestartDead_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	if err := r.RestartDead("no-such-task"); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

// TestHeraReviveRunner_RestartDead_EndToEnd pins the dead-session revive path:
// a task with no live session is started fresh, and the task row is flipped
// to in_progress with the new PID recorded — mirroring handleRestartTask's
// REST behavior (internal/api/handlers.go), the daemon-side counterpart this
// adapter reuses in spirit.
func TestHeraReviveRunner_RestartDead_EndToEnd(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task, _ := seedHeraReviveWorker(t, database, t.TempDir(), "mission")
	task.Status = model.StatusInReview
	testutil.NoError(t, database.Update(task))

	runner := agent.NewRunner(nil) // no session ever started for this task
	cfg := recycleTestConfig()
	r := NewHeraReviveRunner(database, runner, func() config.Config { return cfg })

	testutil.NoError(t, r.RestartDead(task.ID))
	t.Cleanup(runner.StopAll)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := runner.Get(task.ID); sess != nil && sess.Alive() {
			updated, err := database.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, updated.Status, model.StatusInProgress)
			if updated.AgentPID == 0 {
				t.Fatal("expected AgentPID to be recorded")
			}
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for RestartDead to start a fresh session")
}
