package agent

import (
	"bytes"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
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

	task := &model.Task{ID: "t1", Name: "test"}
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

	task := &model.Task{ID: "t2", Name: "test"}
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

func TestRunner_StopAndRunning(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 60", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "t3", Name: "test"}
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

	task := &model.Task{ID: "t-stop", Name: "test"}
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

	task := &model.Task{ID: "t-natural", Name: "test"}
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

func TestRunner_StopNotFound(t *testing.T) {
	r := NewRunner(nil)
	if err := r.Stop("nonexistent"); err != ErrSessionNotFound {
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

	task := &model.Task{ID: "idle-t1", Name: "test"}
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

	// Simulate the session having old output
	sess := r.Get("idle-t1")
	sess.mu.Lock()
	sess.lastOutput = time.Now().Add(-5 * time.Second)
	sess.mu.Unlock()

	idle = r.Idle()
	if len(idle) != 1 || idle[0] != "idle-t1" {
		t.Errorf("expected [idle-t1], got %v", idle)
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

	task := &model.Task{ID: "wd-t1", Name: "test"}
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

	task := &model.Task{ID: "hs-1", Name: "test"}
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

	task1 := &model.Task{ID: "sa-1", Name: "test1"}
	task2 := &model.Task{ID: "sa-2", Name: "test2"}

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

func TestRunner_Detach_NoSession(t *testing.T) {
	r := NewRunner(nil)
	// Should not panic when detaching a nonexistent session
	r.Detach("nonexistent")
}

func TestRunner_Attach_NoSession(t *testing.T) {
	r := NewRunner(nil)
	err := r.Attach("nonexistent", &bytes.Buffer{}, &bytes.Buffer{})
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}
