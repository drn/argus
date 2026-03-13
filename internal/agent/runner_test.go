package agent

import (
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
	r := NewRunner(func(taskID string, err error) {
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
