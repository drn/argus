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
	deadline := time.Now().Add(2 * time.Second)
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

	task := &model.Task{ID: "t1", Name: "test-task", Backend: "test"}
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
