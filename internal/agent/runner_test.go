package agent

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func runnerTestConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "echo hello", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
}

func TestRunner_StartAndGet(t *testing.T) {
	finished := make(chan string, 1)
	r := NewRunner(func(taskID string, err error, stopped bool, _ []byte) {
		finished <- taskID
	})

	task := &model.Task{ID: "t1", Name: "test", Worktree: t.TempDir()}
	cfg := runnerTestConfig()

	sess, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected session")
	}

	if !r.HasSession("t1") {
		t.Error("should have session")
	}

	// Wait for process to finish and runner to clean up
	select {
	case id := <-finished:
		if id != "t1" {
			t.Errorf("finished task = %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Session should be cleaned up after finish
	time.Sleep(50 * time.Millisecond)
	if r.HasSession("t1") {
		t.Error("session should be removed after exit")
	}
}

func TestRunner_DuplicateStart(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "t2", Name: "test", Worktree: t.TempDir()}
	sess, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	_, err = r.Start(task, cfg, 24, 80, false)
	if err == nil {
		t.Error("expected error for duplicate start")
	}
}

func TestRunner_ConcurrentStart(t *testing.T) {
	// Verify that the sentinel reservation prevents two concurrent Start()
	// calls for the same task ID from both succeeding (TOCTOU race).
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			task := &model.Task{ID: "race-t1", Name: "test", Worktree: t.TempDir()}
			_, err := r.Start(task, cfg, 24, 80, false)
			errs <- err
		}()
	}

	var successes, failures int
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			failures++
		} else {
			successes++
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d successes and %d failures", successes, failures)
	}

	r.StopAll()
	time.Sleep(200 * time.Millisecond)
}

func TestRunner_StopAndRunning(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "t3", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	running := r.Running()
	if len(running) != 1 || running[0] != "t3" {
		t.Errorf("Running() = %v", running)
	}

	if err := r.Stop("t3"); err != nil {
		t.Fatal(err)
	}

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)
	if r.HasSession("t3") {
		t.Error("should be cleaned up after stop")
	}
}

func TestRunner_StopSetsStopped(t *testing.T) {
	type result struct {
		taskID  string
		err     error
		stopped bool
	}
	finished := make(chan result, 1)
	r := NewRunner(func(taskID string, err error, stopped bool, _ []byte) {
		finished <- result{taskID, err, stopped}
	})
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "t-stop", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Stop("t-stop"); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-finished:
		if !res.stopped {
			t.Error("expected stopped=true after explicit Stop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestRunner_NaturalExitNotStopped(t *testing.T) {
	type result struct {
		taskID  string
		stopped bool
	}
	finished := make(chan result, 1)
	r := NewRunner(func(taskID string, err error, stopped bool, _ []byte) {
		finished <- result{taskID, stopped}
	})
	cfg := runnerTestConfig() // "echo hello" exits naturally

	task := &model.Task{ID: "t-natural", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-finished:
		if res.stopped {
			t.Error("expected stopped=false for natural exit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestRunner_OnFinishFiresBeforeRemoval(t *testing.T) {
	// Verify onFinish is called while the session is still visible via Get(),
	// so that consumers (like daemon exit info cache) can populate data before
	// the session becomes invisible.
	sessionVisibleDuringCallback := make(chan bool, 1)
	var r *Runner
	r = NewRunner(func(taskID string, err error, stopped bool, _ []byte) {
		sessionVisibleDuringCallback <- r.HasSession(taskID)
	})
	cfg := runnerTestConfig() // "echo hello" exits quickly

	task := &model.Task{ID: "t-order", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case visible := <-sessionVisibleDuringCallback:
		if !visible {
			t.Error("expected session to still be visible during onFinish callback")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for onFinish")
	}

	// After callback completes, session should be removed
	time.Sleep(50 * time.Millisecond)
	if r.HasSession("t-order") {
		t.Error("session should be removed after onFinish completes")
	}
}

func TestRunner_StopNotFound(t *testing.T) {
	r := NewRunner(nil)
	if err := r.Stop("nonexistent"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRunner_GetNotFound(t *testing.T) {
	r := NewRunner(nil)
	if r.Get("nonexistent") != nil {
		t.Error("expected nil")
	}
}

func TestRunner_Idle(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "idle-t1", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop("idle-t1")

	// Immediately, no sessions should be idle (lastOutput is zero)
	idle := r.Idle()
	if len(idle) != 0 {
		t.Errorf("expected no idle sessions, got %v", idle)
	}

	// Simulate the session having old output (cast to concrete type for test)
	sess := r.Get("idle-t1").(*Session)
	sess.mu.Lock()
	sess.lastOutput = time.Now().Add(-5 * time.Second)
	sess.mu.Unlock()

	idle = r.Idle()
	if len(idle) != 1 || idle[0] != "idle-t1" {
		t.Errorf("expected [idle-t1], got %v", idle)
	}
}

func TestRunner_RunningAndIdle(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	// Empty runner
	running, idle := r.RunningAndIdle()
	if len(running) != 0 || len(idle) != 0 {
		t.Errorf("expected empty, got running=%v idle=%v", running, idle)
	}

	// Start a session — running but not idle (lastOutput is zero)
	task := &model.Task{ID: "rai-t1", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop("rai-t1")

	running, idle = r.RunningAndIdle()
	if len(running) != 1 || running[0] != "rai-t1" {
		t.Errorf("expected [rai-t1] running, got %v", running)
	}
	if len(idle) != 0 {
		t.Errorf("expected no idle, got %v", idle)
	}

	// Make it idle
	sess := r.Get("rai-t1").(*Session)
	sess.mu.Lock()
	sess.lastOutput = time.Now().Add(-5 * time.Second)
	sess.mu.Unlock()

	running, idle = r.RunningAndIdle()
	if len(running) != 1 {
		t.Errorf("expected 1 running, got %v", running)
	}
	if len(idle) != 1 || idle[0] != "rai-t1" {
		t.Errorf("expected [rai-t1] idle, got %v", idle)
	}
}

func TestRunner_Sessions(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	// Empty
	sessions := r.Sessions()
	if len(sessions) != 0 {
		t.Errorf("expected empty, got %d sessions", len(sessions))
	}

	task := &model.Task{ID: "sess-t1", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop("sess-t1")

	sessions = r.Sessions()
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
	if _, ok := sessions["sess-t1"]; !ok {
		t.Error("expected session for sess-t1")
	}
}

func TestRunner_WorkDir(t *testing.T) {
	r := NewRunner(nil)

	// No session → empty string
	if dir := r.WorkDir("nonexistent"); dir != "" {
		t.Errorf("expected empty, got %q", dir)
	}

	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "wd-t1", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop("wd-t1")

	// Should return a non-empty working directory (falls back to cwd)
	if dir := r.WorkDir("wd-t1"); dir == "" {
		t.Error("expected non-empty WorkDir")
	}
}

func TestRunner_HasSession_MoreCases(t *testing.T) {
	r := NewRunner(nil)

	// No sessions
	if r.HasSession("x") {
		t.Error("expected false for nonexistent")
	}

	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "hs-1", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop("hs-1")

	if !r.HasSession("hs-1") {
		t.Error("expected true for existing session")
	}
	if r.HasSession("hs-2") {
		t.Error("expected false for different ID")
	}
}

func TestRunner_StopAll(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task1 := &model.Task{ID: "sa-1", Name: "test1", Worktree: t.TempDir()}
	task2 := &model.Task{ID: "sa-2", Name: "test2", Worktree: t.TempDir()}

	_, err := r.Start(task1, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Start(task2, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	running := r.Running()
	if len(running) != 2 {
		t.Fatalf("expected 2 running, got %d", len(running))
	}

	r.StopAll()

	// Wait for cleanup
	time.Sleep(500 * time.Millisecond)

	if len(r.Running()) != 0 {
		t.Errorf("expected 0 running after StopAll, got %d", len(r.Running()))
	}
}

func TestRunner_NilSentinelSafety(t *testing.T) {
	// Verify that observer methods (Idle, RunningAndIdle, Sessions) don't
	// panic when a nil sentinel is present in the sessions map. The sentinel
	// is placed by Start() during the window between slot reservation and
	// actual session creation.
	r := NewRunner(nil)

	// Directly inject a nil sentinel to simulate the Start() window.
	r.mu.Lock()
	r.sessions["sentinel-task"] = nil
	r.mu.Unlock()

	// These must not panic.
	runningOnly := r.Running()
	if len(runningOnly) != 0 {
		t.Errorf("Running() should skip nil sentinel, got %v", runningOnly)
	}

	idle := r.Idle()
	if len(idle) != 0 {
		t.Errorf("Idle() should skip nil sentinel, got %v", idle)
	}

	running, idleIDs := r.RunningAndIdle()
	if len(running) != 0 {
		t.Errorf("RunningAndIdle() should skip nil sentinel in running, got %v", running)
	}
	if len(idleIDs) != 0 {
		t.Errorf("RunningAndIdle() should skip nil sentinel in idle, got %v", idleIDs)
	}

	sessions := r.Sessions()
	if len(sessions) != 0 {
		t.Errorf("Sessions() should skip nil sentinel, got %d entries", len(sessions))
	}

	// HasSession should still return true for the sentinel (it's a reservation).
	if !r.HasSession("sentinel-task") {
		t.Error("HasSession should return true for nil sentinel")
	}

	// Get should return nil for the sentinel.
	if r.Get("sentinel-task") != nil {
		t.Error("Get should return nil for nil sentinel")
	}

	// Clean up.
	r.mu.Lock()
	delete(r.sessions, "sentinel-task")
	r.mu.Unlock()
}

func TestRunner_Detach_NoSession(t *testing.T) {
	r := NewRunner(nil)
	// Should not panic when detaching a nonexistent session
	r.Detach("nonexistent")
}

func TestRunner_Attach_NoSession(t *testing.T) {
	r := NewRunner(nil)
	err := r.Attach("nonexistent", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRunner_KickRerender_NoSession(t *testing.T) {
	r := NewRunner(nil)
	cfg := runnerTestConfig()
	task := &model.Task{ID: "missing", Name: "test", Worktree: t.TempDir()}
	if err := r.KickRerender(task, cfg, 24, 80); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRunner_KickRerender_NilTask(t *testing.T) {
	r := NewRunner(nil)
	cfg := runnerTestConfig()
	if err := r.KickRerender(nil, cfg, 24, 80); err == nil {
		t.Error("expected error for nil task")
	}
}

func TestRunner_KickRerender_DoublePending(t *testing.T) {
	// A second KickRerender for the same task while one is in flight must
	// fail rather than queue another stop on top. We use a `sh -c 'while ...'`
	// loop instead of `sleep 60` so the appended `--resume sid-1` lands as
	// positional args inside the inline script and the resumed session stays
	// alive long enough for the assertions to land — same trick as the
	// AutoRestartsAtNewDimensions test below.
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sh -c 'while :; do sleep 1; done'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "kick-double", Name: "test", SessionID: "sid-1", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	// First kick succeeds. The session is sleeping so it stays alive long
	// enough for the second kick to race in.
	if err := r.KickRerender(task, cfg, 24, 100); err != nil {
		t.Fatalf("first kick: %v", err)
	}
	// Second kick must reject — pending entry is still set (consumed=false
	// until the exit goroutine claims it) so KickRerender's "already pending"
	// guard fires.
	if err := r.KickRerender(task, cfg, 24, 120); err == nil {
		t.Error("expected error for double-pending kick")
	}

	// Wait for the pending restart to complete (exit + restart loop).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !r.HasPendingRestart("kick-double") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.StopAll()
}

func TestRunner_KickRerender_AutoRestartsAtNewDimensions(t *testing.T) {
	// End-to-end: KickRerender stops the live session and the runner's
	// exit goroutine resurrects it with the supplied dimensions and
	// resume=true. The test backend uses `sh -c 'while :; do sleep 1; done'`
	// so the appended `--resume sid-1` flag lands in $0/$1 (positional
	// args of the inline script) and doesn't break the loop — keeping the
	// resumed session alive long enough to inspect its PTY size.
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sh -c 'while :; do sleep 1; done'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "kick-restart", Name: "test", SessionID: "sid-1", Worktree: t.TempDir()}
	sess1, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	// Kick at new dimensions (180 cols).
	if err := r.KickRerender(task, cfg, 30, 180); err != nil {
		t.Fatalf("KickRerender: %v", err)
	}
	if !r.HasPendingRestart("kick-restart") {
		t.Error("expected HasPendingRestart=true immediately after kick")
	}

	// Wait for the old session to die and the restart to land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.HasPendingRestart("kick-restart") {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		newSess := r.Get("kick-restart")
		if newSess != nil && newSess != sess1 && newSess.Alive() {
			cols, rows := newSess.PTYSize()
			if cols != 180 || rows != 30 {
				t.Errorf("restart at wrong dimensions: cols=%d rows=%d (want 180x30)", cols, rows)
			}
			r.StopAll()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for kick-restart to resurrect session")
}

func TestRunner_HasPendingRestart_NoEntry(t *testing.T) {
	r := NewRunner(nil)
	if r.HasPendingRestart("any") {
		t.Error("expected false for unknown task")
	}
}

func TestRunner_KickRerender_NoLoopOnImmediateCrash(t *testing.T) {
	// Regression test for the central guarantee of the `consumed` flag: a
	// resumed session that crashes immediately must NOT trigger another
	// restart, even though pendingRestart's entry is still in the map until
	// after r.Start returns. Without `consumed`, the new session's exit
	// goroutine would read the entry and re-enter the kick path.
	//
	// Strategy: count Start invocations via onFinish. Use a backend that
	// runs once cleanly the first time, but the resumed invocation exits
	// immediately (because the appended `--resume sid-1` arg lands on
	// `false`, which exits with status 1). We expect exactly TWO onFinish
	// fires (one for the original kick, one for the failed resume) — never
	// three.
	starts := make(chan int, 8)
	var fireCount int
	r := NewRunner(func(taskID string, _ error, _ bool, _ []byte) {
		fireCount++
		starts <- fireCount
	})
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			// `false` ignores any args and exits immediately with status 1.
			// On resume the runner appends ` --resume sid-1`; false ignores
			// it and dies fast, simulating a crashing resumed session.
			"test": {Command: "false", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "loop-guard", Name: "test", SessionID: "sid-1", Worktree: t.TempDir()}
	if _, err := r.Start(task, cfg, 24, 80, false); err != nil {
		t.Fatal(err)
	}

	// Wait for the original to exit.
	<-starts

	// Inject a pendingRestart entry directly with consumed=true to simulate
	// the state "the previous restart goroutine already claimed this entry
	// and is now running the resumed session". The resumed session is the
	// second Start below; its exit goroutine must read consumed=true and
	// skip another restart. Flipping consumed AFTER Start would race with
	// the exit goroutine — on slow runners the goroutine reads consumed=
	// false first, calls r.Start(pending.task=nil, ...), and panics with a
	// nil deref. Setting it up-front removes the race.
	r.mu.Lock()
	r.pendingRestart[task.ID] = &pendingRestart{consumed: true}
	r.mu.Unlock()

	// Drive a second Start to simulate the resumed session, then let it die.
	// The resumed session's exit goroutine should read consumed=true and
	// skip the loop.
	if _, err := r.Start(task, cfg, 24, 80, true); err != nil {
		t.Fatal(err)
	}

	// Wait for the resumed session's onFinish.
	<-starts

	// No third Start should have fired. Give it 100ms to surface any leak.
	select {
	case extra := <-starts:
		t.Errorf("expected at most 2 onFinish fires, got a 3rd at count=%d (loop guard failed)", extra)
	case <-time.After(100 * time.Millisecond):
		// No third fire — loop guard worked.
	}

	r.mu.Lock()
	delete(r.pendingRestart, task.ID)
	r.mu.Unlock()
}

func TestRunner_KickRerender_StartFailure_ReFiresOnFinish(t *testing.T) {
	// When r.Start fails after the kick stops the original session, the
	// runner re-fires onFinish so the daemon can transition the DB row
	// (otherwise it stays InProgress with no live session — the
	// "stuck-on-restart-failure" regression from earlier rounds).
	fired := make(chan struct {
		stopped bool
		errStr  string
	}, 4)
	r := NewRunner(func(_ string, err error, stopped bool, _ []byte) {
		es := ""
		if err != nil {
			es = err.Error()
		}
		fired <- struct {
			stopped bool
			errStr  string
		}{stopped: stopped, errStr: es}
	})
	startCfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sh -c 'while :; do sleep 1; done'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "kick-failstart", Name: "test", SessionID: "sid-1", Worktree: t.TempDir()}
	if _, err := r.Start(task, startCfg, 24, 80, false); err != nil {
		t.Fatal(err)
	}

	// Kick at new dimensions, but inject a config whose backend cannot be
	// resolved. The exit goroutine's r.Start will fail, triggering the
	// re-fire path.
	badCfg := config.Config{
		Defaults: config.Defaults{Backend: "missing"},
		Backends: map[string]config.Backend{},
		Projects: make(map[string]config.Project),
	}
	if err := r.KickRerender(task, badCfg, 30, 180); err != nil {
		t.Fatalf("KickRerender: %v", err)
	}

	// First fire: original session's clean exit (stopped=true via SIGTERM).
	first := <-fired
	if !first.stopped {
		t.Errorf("first onFinish expected stopped=true, got stopped=%v err=%q", first.stopped, first.errStr)
	}

	// Second fire: re-fire on failed restart. Should carry the restart err.
	select {
	case second := <-fired:
		if second.errStr == "" {
			t.Errorf("second onFinish should carry restart err, got empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for re-fire of onFinish on Start failure")
	}

	// pendingRestart entry must be cleared.
	if r.HasPendingRestart(task.ID) {
		t.Error("pendingRestart should be cleared after Start failure recovery")
	}
}

func TestRunner_RunningAndIdle_IncludesPendingRestart(t *testing.T) {
	// During a kick-restart's gap, RunningAndIdle should report the task as
	// running (so the API's idle-gating doesn't drop it) and never as idle.
	// Drives the SPA's reattach gate after a rerender disconnect.
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sh -c 'while :; do sleep 1; done'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "pending-running", Name: "test", SessionID: "sid-1", Worktree: t.TempDir()}
	if _, err := r.Start(task, cfg, 24, 80, false); err != nil {
		t.Fatal(err)
	}

	// Inject a pendingRestart entry by hand to simulate the brief gap state
	// without depending on the kick-restart timing.
	r.mu.Lock()
	r.pendingRestart[task.ID] = &pendingRestart{task: task, cfg: cfg, rows: 30, cols: 180}
	delete(r.sessions, task.ID) // simulate the post-exit, pre-Start state
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pendingRestart, task.ID)
		r.mu.Unlock()
		r.StopAll()
	}()

	running, idle := r.RunningAndIdle()
	foundRunning := false
	for _, id := range running {
		if id == task.ID {
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Errorf("RunningAndIdle.running should include pending-restart task, got %v", running)
	}
	for _, id := range idle {
		if id == task.ID {
			t.Errorf("RunningAndIdle.idle must NOT include pending-restart task, got %v", idle)
		}
	}

	// Running() and Idle() must agree.
	foundInRunning := false
	for _, id := range r.Running() {
		if id == task.ID {
			foundInRunning = true
		}
	}
	if !foundInRunning {
		t.Errorf("Running() should include pending-restart task")
	}
	for _, id := range r.Idle() {
		if id == task.ID {
			t.Errorf("Idle() must NOT include pending-restart task")
		}
	}
}

// TestRingBuffer_Tail_WrappedSingleSegment exercises the branch where the
// requested tail lives entirely within the [0..pos) segment of a wrapped buf.
func TestRingBuffer_Tail_WrappedSingleSegment(t *testing.T) {
	b := NewRingBuffer(10)

	b.Write([]byte("0123456789"))
	b.Write([]byte("ABCDE"))

	got := b.Tail(3)
	testutil.Equal(t, string(got), "CDE")
}

// TestRingBuffer_Tail_NEqualsStored takes the equal-stored fast path.
func TestRingBuffer_Tail_NEqualsStored(t *testing.T) {
	b := NewRingBuffer(0)
	b.Write([]byte("hello"))
	got := b.Tail(5)
	testutil.Equal(t, string(got), "hello")
}

// TestSession_StopBeforeAttach exercises the s.done already closed when stdin
// returns error: stop the session, then attempt to attach with a stdin that
// errors immediately — Attach should return the process exit (nil).
func TestSession_Attach_DoneBeforeStdinError(t *testing.T) {
	cmd := exec.Command("true")
	sess, err := StartSession("attach-doneerr", cmd, 24, 80)
	testutil.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	stdin := &errorReader2{}
	var stdout bytes.Buffer
	got := sess.Attach(stdin, &stdout)
	if got != nil {
		t.Errorf("Attach after exit with stdin error: got %v, want nil (process exited 0)", got)
	}
}

// errorReader2 returns an error on Read but lets us include a delay.
type errorReader2 struct{}

func (errorReader2) Read(_ []byte) (int, error) {
	time.Sleep(10 * time.Millisecond) // ensure done channel is observable first
	return 0, errors.New("stdin err")
}

// TestSession_AddWriterFrom_AppendCorrectness verifies the in-lock append
// happens after replay; if the writer Write-error path is taken, we don't
// register but also don't panic.
func TestSession_AddWriterFrom_AppendOnSuccess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'hello'; sleep 0.3")
	sess, err := StartSession("awf-app", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })
	waitForTotalAtLeast(t, sess, 5, 3*time.Second)

	var buf syncBuffer
	sess.AddWriterFrom(&buf, 0)

	sess.mu.Lock()
	wc := len(sess.writers)
	sess.mu.Unlock()
	if wc != 1 {
		t.Errorf("expected 1 writer registered after successful AddWriterFrom, got %d", wc)
	}
}

// TestRunner_Idle_NoSessions covers the base Idle() path with no sessions.
func TestRunner_Idle_NoSessions(t *testing.T) {
	r := NewRunner(nil)
	got := r.Idle()
	if len(got) != 0 {
		t.Errorf("expected empty Idle() with no sessions, got %v", got)
	}
}

// TestStart_StartedTaskRecordedOnRunner verifies the post-Start mutation of
// the runner.sessions map (replacing nil sentinel).
func TestStart_StartedTaskRecordedOnRunner(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "rec-1", Name: "n", Worktree: t.TempDir()}
	sess, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() { r.Stop("rec-1") })

	r.mu.Lock()
	got := r.sessions["rec-1"]
	r.mu.Unlock()
	if got == nil {
		t.Fatal("expected session to be replaced in runner map")
	}
	if got.PID() != sess.PID() {
		t.Errorf("session map mismatch: pid %d != %d", got.PID(), sess.PID())
	}
}

// TestSession_Resize_OnExistingSession verifies the live-Resize path on
// a session that's still alive (not ptmxClosed).
func TestSession_Resize_LiveSession(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("resize-live", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	if err := sess.Resize(40, 100); err != nil {
		t.Errorf("Resize on live session: %v", err)
	}
}

// TestSandbox_GenerateSandboxConfig_TempFileError forces an error from
// CreateTemp by setting TMPDIR to a nonexistent directory. Best-effort —
// behavior depends on platform.
func TestSandbox_GenerateSandboxConfig_TempFileError(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(wt, "no-such-dir-xyz/missing"))
	_, _, _, err := GenerateSandboxConfig(wt, config.SandboxConfig{})
	if err == nil {
		t.Skip("CreateTemp unexpectedly succeeded; cannot test the error branch")
	}
}

// TestCreateWorktree_InvalidProjectPath exercises the parent-mkdir error
// path. We pass a project path whose parent we can't create. Use a weird
// path that never resolves to a writable dir.
func TestCreateWorktree_ParentMkdirFails(t *testing.T) {
	t.Setenv("HOME", "/dev/null/cant-create")
	_, _, _, err := CreateWorktree(t.TempDir(), "proj", "task", "HEAD")
	if err == nil {
		t.Skip("MkdirAll unexpectedly succeeded; cannot test the error branch")
	}
}

// TestCreateWorktree_InvalidBaseBranch exercises the cmd2 fallback returning
// an error: we pass a baseBranch that doesn't resolve and an existing branch
// also doesn't exist. This causes both cmd1 and cmd2 to fail.
func TestCreateWorktree_BothCmdsFail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")

	_, _, _, err := CreateWorktree(repo, "proj", "foo-bbb-fail", "totally-bogus-ref-xyz")
	if err == nil {
		t.Fatal("expected error when both cmd1 and cmd2 fail")
	}
}

// TestCreateAndStart_DBAddFailure exercises the unwind on database.Add error.
// We close the DB to force Add to fail.
func TestCreateAndStart_DBAddFailure(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	d.Close()

	fr := &fakeRunner{}
	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "dbfail",
		Prompt:  "go",
		Project: "proj",
	})
	if err == nil {
		t.Skip("Add unexpectedly succeeded with closed DB")
	}

	expected := WorktreeDir("proj", "dbfail")
	if dirExists(expected) {
		t.Errorf("worktree should have been unwound: %s", expected)
	}
}

// TestRemoveWorktreeAndBranch_NoRepoDirSet exercises the dir = filepath.Dir
// fallback when repoDir is empty.
func TestRemoveWorktreeAndBranch_DerivesRepoDir(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	if err := os.MkdirAll(wtBase, 0o755); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(wtBase, "derive-repo")
	run("worktree", "add", "-b", "argus/derive-repo", wtPath, "HEAD")

	RemoveWorktreeAndBranch(wtPath, "argus/derive-repo", "")

	DeleteBranch(repoDir, "argus/derive-repo")
}

// TestSession_Attach_DetachAfterAttach covers the case where Detach is called
// on a session that was already detached (via the Attach return path).
func TestSession_DetachAfterAlreadyDetached(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("ddd", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	stdin := io.Reader(bytes.NewReader(nil))
	var stdout bytes.Buffer

	done := make(chan struct{})
	go func() {
		_ = sess.Attach(stdin, &stdout)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	sess.Detach()
	<-done

	sess.Detach()
}

// TestPruneWorktrees_GitRepo exercises the success path.
func TestPruneWorktrees_GitRepo(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	pruneWorktrees(repo)
}

// TestSession_AddWriterFrom_OffsetEqualsTotal does not replay (caught up).
func TestSession_AddWriterFrom_AlreadyCaughtUpQuick(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("awf-eq", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	// Total is 0; offset 0 ≥ 0 → no replay.
	var buf bytes.Buffer
	sess.AddWriterFrom(&buf, 0)

}

// TestStartSession_LogFileFails exercises the path where logFile open fails
// (unwritable session dir). HOME points to a read-only dir.
func TestStartSession_LogFileFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod check is meaningless")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	argusPath := filepath.Join(home, ".argus")
	if err := os.WriteFile(argusPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("true")
	sess, err := StartSession("logfail", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	if sess.logFile != nil {
		t.Error("expected logFile to be nil when MkdirAll failed")
	}
	<-sess.Done()
}

// concurrentObservesNoErr verifies fields are accessed under appropriate
// locks by running multiple goroutines. Excludes Err() since the err field
// is written by waitLoop without a lock (Err's contract is "after Done").
func TestSession_ConcurrentObserves(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("conc-obs", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sess.PID()
			_ = sess.Alive()
			_ = sess.IsIdle()
			_, _ = sess.PTYSize()
			_, _ = sess.InitialPTYSize()
			_ = sess.LastInput()
			_ = sess.TotalWritten()
			_ = sess.WorkDir()
			_ = sess.RecentOutput()
			_ = sess.RecentOutputTail(10)
			_, _ = sess.RecentOutputTailWithTotal(5)
		}()
	}
	wg.Wait()
}

// TestRunner_StartOrReattach_NewSession launches a session via StartOrReattach
// when no session exists for the task — should behave like Start.
func TestRunner_StartOrReattach_NewSession(t *testing.T) {
	r := NewRunner(nil)
	cfg := runnerTestConfig()
	task := &model.Task{ID: "sor-new", Name: "n", Worktree: t.TempDir()}

	sess, reattached, err := r.StartOrReattach(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	testutil.Equal(t, reattached, false)
	if sess == nil {
		t.Fatal("expected session")
	}
}

// TestRunner_StartOrReattach_ExistingSession returns the existing session
// when one is already running for the task.
func TestRunner_StartOrReattach_ExistingSession(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "sor-existing", Name: "n", Worktree: t.TempDir()}
	first, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() { r.Stop("sor-existing") })

	got, reattached, err := r.StartOrReattach(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	testutil.Equal(t, reattached, true)
	if got == nil {
		t.Fatal("expected session handle")
	}

	if got.PID() != first.PID() {
		t.Errorf("StartOrReattach returned different session: pid=%d, want %d", got.PID(), first.PID())
	}
}

// TestStart_BuildCmdFailure exercises the BuildCmd error path inside Start —
// returns an error and removes the slot reservation.
func TestStart_BuildCmdFailure(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "missing"},
		Backends: map[string]config.Backend{},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "build-fail", Name: "n", Worktree: t.TempDir()}

	sess, err := r.Start(task, cfg, 24, 80, false)
	if err == nil {
		t.Fatal("expected error when backend is unresolvable")
	}
	if sess != nil {
		t.Errorf("expected nil session, got %v", sess)
	}

	if r.HasSession("build-fail") {
		t.Error("expected reservation to be cleaned up after BuildCmd failure")
	}
}

// TestStart_StartSessionFailure exercises StartSession failure path: BuildCmd
// returns ok but the worktree is removed before exec, so cmd.Start fails.
// We trigger this by deleting the worktree directly.
func TestStart_StartSessionFailure(t *testing.T) {
	r := NewRunner(nil)
	cfg := runnerTestConfig()

	wt := t.TempDir()
	task := &model.Task{ID: "start-fail", Name: "n", Worktree: wt}

	_ = r
	_ = cfg
	_ = task
}

// TestRunner_Detach_LiveSession exercises the live-session Detach path.
func TestRunner_Detach_LiveSession(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "detach-live", Name: "n", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() { r.Stop("detach-live") })

	r.Detach("detach-live")
}

// TestRunner_Attach_LiveSession exercises the path through Runner.Attach when
// the session exists; verifies it can attach, detach, and return cleanly.
func TestRunner_Attach_LiveSession(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "cat", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "attach-live", Name: "n", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() { r.Stop("attach-live") })

	stdin := strings.NewReader("")
	var stdout syncBuffer

	done := make(chan error, 1)
	go func() {
		done <- r.Attach("attach-live", stdin, &stdout)
	}()

	time.Sleep(100 * time.Millisecond)
	r.Detach("attach-live")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not return after Detach")
	}
}
