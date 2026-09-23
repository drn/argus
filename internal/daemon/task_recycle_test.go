package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// seedRecyclePlainTask creates a plain (non-hera) task for taskRecycleRunner
// tests, mirroring seedHeraRecycleCoordinator (recycle_test.go) but with no
// orchestrator/role — task_recycle works on any argus task.
func seedRecyclePlainTask(t *testing.T, database *db.DB, worktree, prompt string) *model.Task {
	t.Helper()
	task := &model.Task{
		ID:       "plain-task",
		Name:     "plain-task",
		Status:   model.StatusInProgress,
		Project:  "test-project",
		Worktree: worktree,
		Backend:  "test",
		Prompt:   prompt,
	}
	testutil.NoError(t, database.Add(task))
	return task
}

func TestBuildTaskRecycleSeedPrompt(t *testing.T) {
	got := buildTaskRecycleSeedPrompt("original mission text", "did X, next do Y")

	testutil.Contains(t, got, "did X, next do Y")
	testutil.Contains(t, got, "original mission text")

	// The handoff note must appear before the original prompt (design.md D5's
	// ordering rationale, mirrored from hera.BuildRecycleSeedPrompt: a fresh
	// session anchors on current state, not a stale "start from scratch"
	// instruction).
	if strings.Index(got, "did X, next do Y") > strings.Index(got, "original mission text") {
		t.Fatal("handoff note must appear before the original prompt")
	}
}

func TestTaskRecycleRunner_Restart_ClearsSessionAndSeedsPrompt(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")
	task.SessionID = "stale-sid-1"
	testutil.NoError(t, database.Update(task))

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	sess1, err := runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(runner.StopAll)

	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })
	testutil.NoError(t, r.restart(task.ID, "the seed prompt"))

	updated, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, updated.SessionID, "")
	testutil.Equal(t, updated.Prompt, "the seed prompt")

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
	t.Fatal("timeout waiting for restart to resurrect the session")
}

func TestTaskRecycleRunner_Restart_NoLiveSession_StartsFresh(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")

	runner := agent.NewRunner(nil) // no session ever started for this task
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })

	testutil.NoError(t, r.restart(task.ID, "the seed prompt"))
	t.Cleanup(runner.StopAll)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := runner.Get(task.ID); sess != nil && sess.Alive() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for restart to start a fresh session when none existed")
}

func TestTaskRecycleRunner_Restart_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })

	if err := r.restart("no-such-task", "seed"); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

// TestTaskRecycleRunner_AwaitIdleAndRestart_NoSessionRestartsImmediately pins
// awaitIdleAndRestart's "missing session counts as idle" behavior (mirrors
// HeraRecycleRunner.IsIdle's same rule): a task with no live session — e.g.
// one that already exited before this goroutine got scheduled — must restart
// on the very first check, not hang waiting for a session that will never
// exist.
func TestTaskRecycleRunner_AwaitIdleAndRestart_NoSessionRestartsImmediately(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")

	runner := agent.NewRunner(nil) // Get(task.ID) returns nil
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })
	r.sleep = func(time.Duration) {}

	r.awaitIdleAndRestart(task.ID, "the seed prompt")
	t.Cleanup(runner.StopAll)

	updated, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, updated.Prompt, "the seed prompt")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess := runner.Get(task.ID); sess != nil && sess.Alive() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for a fresh session to start")
}

// TestTaskRecycleRunner_AwaitIdleAndRestart_NeverIdleGivesUp pins the timeout
// guard: a session that never goes idle (recycleTestConfig's backend emits no
// output at all, so Session.IsIdle's "still starting up" branch never
// clears) must not restart the task, and the poll loop must not hang
// forever.
func TestTaskRecycleRunner_AwaitIdleAndRestart_NeverIdleGivesUp(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")
	task.SessionID = "stale-sid-1"
	testutil.NoError(t, database.Update(task))

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	_, err = runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(runner.StopAll)

	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })
	r.sleep = func(time.Duration) {}
	r.idleTimeout = 0 // expire on the very first deadline check

	r.awaitIdleAndRestart(task.ID, "the seed prompt")

	// Restart must NOT have happened: SessionID/Prompt unchanged.
	updated, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, updated.SessionID, "stale-sid-1")
	testutil.Equal(t, updated.Prompt, "original mission")

	// The seed prompt must be persisted to task_meta instead of silently
	// discarded — the MCP call already told the caller a fresh session
	// would start "shortly."
	meta, err := database.ListMeta(task.ID, taskRecycleMetaNamespace)
	testutil.NoError(t, err)
	found := false
	for _, e := range meta {
		if e.Key == taskRecycleMetaKeyTimedOutPrompt {
			testutil.Equal(t, e.Value, "the seed prompt")
			found = true
		}
	}
	if !found {
		t.Fatal("expected the timed-out seed prompt to be persisted to task_meta")
	}
}

func TestTaskRecycleRunner_Recycle_SchedulesRestart(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")

	runner := agent.NewRunner(nil) // no session ever started -> idle immediately
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })
	r.sleep = func(time.Duration) {}
	// Run synchronously so the assertions below don't race a background goroutine.
	r.spawn = func(f func()) { f() }

	testutil.NoError(t, r.Recycle(task.ID, "did X, next do Y"))
	t.Cleanup(runner.StopAll)

	updated, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Contains(t, updated.Prompt, "did X, next do Y")
	testutil.Contains(t, updated.Prompt, "original mission")
}

func TestTaskRecycleRunner_Recycle_UnknownTaskErrors(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })

	if err := r.Recycle("no-such-task", "note"); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

// TestTaskRecycleRunner_Recycle_UnknownTaskReleasesInFlight pins that a
// failed Recycle (task lookup error) releases its inFlight reservation —
// otherwise an unknown-task typo would permanently wedge that task ID
// against ever recycling again, even once a task with that ID exists.
func TestTaskRecycleRunner_Recycle_UnknownTaskReleasesInFlight(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })

	if err := r.Recycle("no-such-task", "note"); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
	if _, stillHeld := r.inFlight.Load("no-such-task"); stillHeld {
		t.Fatal("a failed Recycle must release its inFlight reservation")
	}
}

// TestTaskRecycleRunner_Recycle_RejectsConcurrentCallsForSameTask pins the
// dedup guard: a second Recycle call for a task that already has one
// in-flight is rejected outright, rather than racing a second
// awaitIdleAndRestart poller against the first (see inFlight's doc comment
// for the stale-seed-prompt race this prevents).
func TestTaskRecycleRunner_Recycle_RejectsConcurrentCallsForSameTask(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	task := seedRecyclePlainTask(t, database, t.TempDir(), "original mission")

	runner := agent.NewRunner(nil)
	cfg := recycleTestConfig()
	r := newTaskRecycleRunner(database, runner, func() config.Config { return cfg })
	// Never actually run the poll loop, so the first call's reservation
	// stays held for the second call to collide with.
	r.spawn = func(func()) {}

	testutil.NoError(t, r.Recycle(task.ID, "first handoff"))

	err = r.Recycle(task.ID, "second handoff")
	if err == nil {
		t.Fatal("expected the second concurrent Recycle call to be rejected")
	}
	testutil.Contains(t, err.Error(), "already in progress")

	// The task row must be untouched by the rejected second call.
	unchanged, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unchanged.Prompt, "original mission")
}
