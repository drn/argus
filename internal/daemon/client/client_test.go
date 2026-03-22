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

	// Poll until output arrives (process must exit AND stream must deliver).
	deadline := time.Now().Add(5 * time.Second)
	var output string
	for time.Now().Before(deadline) {
		output = string(sess.RecentOutput())
		if strings.Contains(output, "hello-from-daemon") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

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
	alive, reachable := rs.isSessionAlive()
	if !reachable {
		t.Error("expected daemon to be reachable")
	}
	if alive {
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
	alive, reachable := rs.isSessionAlive()
	if !reachable {
		t.Error("expected daemon to be reachable")
	}
	if alive {
		t.Error("expected isSessionAlive to return false for nonexistent session")
	}
}

func TestGet_ExitingSession(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Start a quick-exit session.
	task := &model.Task{ID: "t-get-exit", Name: "get-exit-test", Backend: "test", Worktree: t.TempDir()}
	_, err = c.Start(task, config.Config{}, 24, 80, false)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var info daemon.SessionInfo
		if e := c.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: "t-get-exit"}, &info); e == nil && !info.Alive {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Remove from local sessions map so Get() queries the daemon.
	c.mu.Lock()
	delete(c.sessions, "t-get-exit")
	c.mu.Unlock()

	// Get() should return nil because !info.Alive, even if PID != 0.
	handle := c.Get("t-get-exit")
	if handle != nil {
		t.Error("expected Get() to return nil for exited session")
	}
}

func TestStreamLost_RemoveSession(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	exitCh := make(chan daemon.ExitInfo, 1)
	c.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
		exitCh <- info
	})

	// Manually add a session to the map.
	c.mu.Lock()
	c.sessions["t-stream-lost"] = newRemoteSession("t-stream-lost", c)
	c.mu.Unlock()

	// Call removeSessionStreamLost — should fire StreamLost=true.
	c.removeSessionStreamLost("t-stream-lost")

	select {
	case info := <-exitCh:
		if !info.StreamLost {
			t.Error("expected StreamLost=true in exit info")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stream lost callback")
	}

	// Session should be removed from map.
	c.mu.Lock()
	_, exists := c.sessions["t-stream-lost"]
	c.mu.Unlock()
	if exists {
		t.Error("expected session to be removed from map")
	}
}

// TestDoneClose_StreamLost verifies that when rs.done is closed externally
// (e.g., Client.Close during daemon restart), the exit callback fires with
// StreamLost=true rather than marking the task as exited.
// We call removeSessionStreamLost directly rather than driving through
// connectStream because triggering the <-rs.done branch requires a live
// daemon with a flaky stream (not feasible in unit tests).
func TestDoneClose_StreamLost(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	exitCh := make(chan daemon.ExitInfo, 1)
	c.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
		exitCh <- info
	})

	// Manually add a session and pre-close done to simulate the
	// Client.Close() → rs.close() path during daemon restart.
	rs := newRemoteSession("t-done-close", c)
	c.mu.Lock()
	c.sessions["t-done-close"] = rs
	c.mu.Unlock()

	// Close done to simulate client shutdown.
	rs.close()

	// removeSessionStreamLost should fire with StreamLost=true.
	// This is what connectStream's <-rs.done case now calls.
	c.removeSessionStreamLost("t-done-close")

	select {
	case info := <-exitCh:
		if !info.StreamLost {
			t.Error("expected StreamLost=true when session closed externally")
		}
		if info.Stopped {
			t.Error("expected Stopped=false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for exit callback")
	}
}

func TestAlive_DaemonDown(t *testing.T) {
	_, sockPath := testSetup(t)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Close the client's RPC connection to simulate daemon being unreachable.
	c.rpc.Close()

	rs := &RemoteSession{taskID: "t-daemon-down", client: c}
	alive, reachable := rs.isSessionAlive()
	if reachable {
		t.Error("expected daemon to be unreachable after closing RPC connection")
	}
	if alive {
		t.Error("expected alive=false when daemon is unreachable")
	}
}
