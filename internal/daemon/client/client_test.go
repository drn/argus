package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

func testSetup(t *testing.T) (*daemon.Daemon, string) {
	t.Helper()
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	database.SetBackend("test", config.Backend{Command: "echo hello-from-daemon"})
	database.SetConfigValue("default.backend", "test")

	d := daemon.New(database)
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	go d.Serve(sockPath)
	t.Cleanup(func() { d.Shutdown() })

	// Wait for socket.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return d, sockPath
}

func TestClient_ConnectAndPing(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// HasSession should return false for nonexistent.
	if c.HasSession("nonexistent") {
		t.Error("expected false for nonexistent session")
	}
}

func TestClient_StartAndGetOutput(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	task := &model.Task{ID: "t1", Name: "test-task", Backend: "test", Worktree: t.TempDir()}
	sess, err := c.Start(task, config.Config{}, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}
	if sess.PID() == 0 {
		t.Error("expected non-zero PID")
	}

	// Wait for process to exit and stream to deliver output.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !sess.Alive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Give stream reader time to populate buffer.
	time.Sleep(200 * time.Millisecond)

	output := string(sess.RecentOutput())
	if !strings.Contains(output, "hello-from-daemon") {
		t.Errorf("expected output to contain 'hello-from-daemon', got %q", output)
	}
}

func TestClient_RunningAndIdle(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Initially no sessions.
	if ids := c.Running(); len(ids) != 0 {
		t.Errorf("expected no running sessions, got %v", ids)
	}
	if ids := c.Idle(); len(ids) != 0 {
		t.Errorf("expected no idle sessions, got %v", ids)
	}
}

func TestClient_StopAll(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// StopAll should not panic with no sessions.
	c.StopAll()
}

func TestClient_SessionExitCallback(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	exitCh := make(chan string, 1)
	c.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
		exitCh <- taskID
	})

	task := &model.Task{ID: "t-exit", Name: "exit-test", Backend: "test", Worktree: t.TempDir()}
	_, err = c.Start(task, config.Config{}, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	// "echo hello" exits quickly — callback should fire.
	select {
	case id := <-exitCh:
		if id != "t-exit" {
			t.Errorf("expected task ID 't-exit', got %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for session exit callback")
	}
}

func TestAlive_Dead(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Create a session for a quick-exit command.
	task := &model.Task{ID: "t-dead", Name: "dead-test", Backend: "test", Worktree: t.TempDir()}
	_, err = c.Start(task, config.Config{}, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit.
	time.Sleep(1 * time.Second)

	// Create a RemoteSession to test isSessionAlive against a dead process.
	rs := &RemoteSession{taskID: "t-dead", client: c}
	if rs.isSessionAlive() {
		t.Error("expected isSessionAlive to return false for exited process")
	}
}

func TestAlive_NoSession(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// isSessionAlive for a session that never existed should return false.
	// SessionStatus returns empty info (Alive=false, PID=0) for unknown task IDs.
	rs := &RemoteSession{taskID: "nonexistent", client: c}
	if rs.isSessionAlive() {
		t.Error("expected isSessionAlive to return false for nonexistent session")
	}
}
