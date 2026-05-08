package daemon

import (
	"encoding/json"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func testDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	d := New(database)

	// Use a temp socket path.
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	return d, sockPath
}

func dialRPC(t *testing.T, sockPath string) *rpc.Client {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Send RPC prefix byte.
	conn.Write([]byte("R"))
	client := jsonrpc.NewClient(conn)
	t.Cleanup(func() { client.Close() })
	return client
}

func dialStream(t *testing.T, sockPath string, taskID string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial stream: %v", err)
	}
	// Send stream prefix byte.
	conn.Write([]byte("S"))
	// Send stream header.
	enc := json.NewEncoder(conn)
	if err := enc.Encode(StreamHeader{TaskID: taskID}); err != nil {
		conn.Close()
		t.Fatalf("encode header: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestDaemon_UpdateSelfEmpty(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)
	var resp UpdateSelfResp
	if err := client.Call("Daemon.UpdateSelf", &Empty{}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == "" {
		t.Error("expected error when source path is unset")
	}
}

func TestDaemon_BootInfo(t *testing.T) {
	d, sockPath := testDaemon(t)

	bootStart := time.Now()
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)
	var resp BootInfoResp
	if err := client.Call("Daemon.BootInfo", &Empty{}, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.BinaryPath == "" {
		t.Error("expected BinaryPath to be populated")
	}
	if resp.BootedAt.Before(bootStart.Add(-time.Second)) || resp.BootedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("BootedAt %v outside expected range", resp.BootedAt)
	}
	// BinaryMtime is best-effort; if the test binary exists, it should be
	// non-zero. If not, that's a pre-existing environment issue, not a bug.
	if resp.BinaryPath != "" {
		if _, err := os.Stat(resp.BinaryPath); err == nil && resp.BinaryMtime.IsZero() {
			t.Error("expected BinaryMtime to be populated when binary exists")
		}
	}
}

func TestDaemon_Ping(t *testing.T) {
	d, sockPath := testDaemon(t)

	go d.Serve(sockPath)
	t.Cleanup(func() { d.Shutdown() })

	// Wait for socket to appear.
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)
	var resp PongResp
	if err := client.Call("Daemon.Ping", &Empty{}, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Error("expected Ping to return OK=true")
	}
}

func TestDaemon_ListSessions_Empty(t *testing.T) {
	d, sockPath := testDaemon(t)

	go d.Serve(sockPath)
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)
	var resp ListResp
	if err := client.Call("Daemon.ListSessions", &Empty{}, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestDaemon_StartAndStop(t *testing.T) {
	d, sockPath := testDaemon(t)

	// Seed a backend config.
	d.db.SetBackend("test", config.Backend{Command: "sleep 60"})
	d.db.SetConfigValue("default.backend", "test")

	go d.Serve(sockPath)
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)

	// Start a session.
	wtDir := t.TempDir()
	var startResp StartResp
	err := client.Call("Daemon.StartSession", &StartReq{
		TaskID:   "t1",
		Backend:  "test",
		Worktree: wtDir,
		Rows:     24,
		Cols:     80,
	}, &startResp)
	if err != nil {
		t.Fatal(err)
	}
	if startResp.PID == 0 {
		t.Error("expected non-zero PID")
	}

	// List sessions.
	var listResp ListResp
	if err := client.Call("Daemon.ListSessions", &Empty{}, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(listResp.Sessions))
	}
	if listResp.Sessions[0].TaskID != "t1" {
		t.Errorf("expected task t1, got %q", listResp.Sessions[0].TaskID)
	}

	// Stop session.
	var stopResp StatusResp
	if err := client.Call("Daemon.StopSession", &TaskIDReq{TaskID: "t1"}, &stopResp); err != nil {
		t.Fatal(err)
	}
	if !stopResp.OK {
		t.Errorf("expected OK, got error: %s", stopResp.Error)
	}

	// Wait for cleanup.
	time.Sleep(200 * time.Millisecond)

	// Session should be gone.
	var listResp2 ListResp
	if err := client.Call("Daemon.ListSessions", &Empty{}, &listResp2); err != nil {
		t.Fatal(err)
	}
	if len(listResp2.Sessions) != 0 {
		t.Errorf("expected 0 sessions after stop, got %d", len(listResp2.Sessions))
	}
}

func TestDaemon_Shutdown(t *testing.T) {
	d, sockPath := testDaemon(t)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Serve(sockPath)
	}()
	waitForSocket(t, sockPath)

	client := dialRPC(t, sockPath)
	var resp StatusResp
	if err := client.Call("Daemon.Shutdown", &Empty{}, &resp); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Serve to return")
	}
}

func TestReadPIDFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		if got := readPIDFile(filepath.Join(dir, "nope.pid")); got != 0 {
			t.Errorf("expected 0 for missing file, got %d", got)
		}
	})

	t.Run("valid pid", func(t *testing.T) {
		p := filepath.Join(dir, "valid.pid")
		os.WriteFile(p, []byte("12345\n"), 0644)
		if got := readPIDFile(p); got != 12345 {
			t.Errorf("expected 12345, got %d", got)
		}
	})

	t.Run("invalid content", func(t *testing.T) {
		p := filepath.Join(dir, "bad.pid")
		os.WriteFile(p, []byte("notanumber"), 0644)
		if got := readPIDFile(p); got != 0 {
			t.Errorf("expected 0 for invalid content, got %d", got)
		}
	})
}

func TestRemoveIfOwnedByPID(t *testing.T) {
	t.Run("removes when owned", func(t *testing.T) {
		dir := t.TempDir()
		sock := filepath.Join(dir, "d.sock")
		pid := filepath.Join(dir, "d.pid")
		os.WriteFile(sock, []byte("x"), 0644)
		os.WriteFile(pid, []byte("999"), 0644)

		removeIfOwnedByPID(sock, pid, 999)

		if _, err := os.Stat(sock); !os.IsNotExist(err) {
			t.Error("socket should have been removed")
		}
		if _, err := os.Stat(pid); !os.IsNotExist(err) {
			t.Error("pid file should have been removed")
		}
	})

	t.Run("skips when not owned", func(t *testing.T) {
		dir := t.TempDir()
		sock := filepath.Join(dir, "d.sock")
		pid := filepath.Join(dir, "d.pid")
		os.WriteFile(sock, []byte("x"), 0644)
		os.WriteFile(pid, []byte("888"), 0644)

		removeIfOwnedByPID(sock, pid, 999) // different PID

		if _, err := os.Stat(sock); os.IsNotExist(err) {
			t.Error("socket should NOT have been removed")
		}
		if _, err := os.Stat(pid); os.IsNotExist(err) {
			t.Error("pid file should NOT have been removed")
		}
	})
}

func TestKillExistingDaemon_DeadProcess(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "d.pid")

	// Write a PID that almost certainly doesn't exist.
	os.WriteFile(pidPath, []byte("2000000000"), 0644)

	// Should not panic — process is dead, killExistingDaemon returns early.
	killExistingDaemon(pidPath)
}

func TestKillExistingDaemon_NoPIDFile(t *testing.T) {
	// Should not panic when PID file doesn't exist.
	killExistingDaemon(filepath.Join(t.TempDir(), "nope.pid"))
}

func TestDaemon_Clip(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)

	// Initially empty.
	var getResp ClipboardGetResp
	if err := c.Call("Daemon.ClipboardGet", &ClipboardGetReq{TaskID: "task1"}, &getResp); err != nil {
		t.Fatal(err)
	}
	if getResp.OK || getResp.Text != "" {
		t.Errorf("expected empty initial state, got %+v", getResp)
	}

	// Set.
	var setResp StatusResp
	if err := c.Call("Daemon.ClipboardSet", &ClipboardSetReq{TaskID: "task1", Text: "hello"}, &setResp); err != nil {
		t.Fatal(err)
	}
	if !setResp.OK || setResp.Error != "" {
		t.Errorf("set failed: %+v", setResp)
	}

	// Get back.
	getResp = ClipboardGetResp{}
	if err := c.Call("Daemon.ClipboardGet", &ClipboardGetReq{TaskID: "task1"}, &getResp); err != nil {
		t.Fatal(err)
	}
	if !getResp.OK || getResp.Text != "hello" {
		t.Errorf("expected hello, got %+v", getResp)
	}

	// Clear.
	var clearResp StatusResp
	if err := c.Call("Daemon.ClipboardClear", &ClipboardClearReq{TaskID: "task1"}, &clearResp); err != nil {
		t.Fatal(err)
	}
	if !clearResp.OK {
		t.Errorf("clear failed: %+v", clearResp)
	}

	// Confirm cleared.
	getResp = ClipboardGetResp{}
	if err := c.Call("Daemon.ClipboardGet", &ClipboardGetReq{TaskID: "task1"}, &getResp); err != nil {
		t.Fatal(err)
	}
	if getResp.OK {
		t.Errorf("expected cleared, got %+v", getResp)
	}
}

func TestDaemon_ClipBig(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)

	// Anything over 1 MiB should be rejected with an error in the RPC response.
	big := make([]byte, (1<<20)+1)
	for i := range big {
		big[i] = 'a'
	}
	var setResp StatusResp
	if err := c.Call("Daemon.ClipboardSet", &ClipboardSetReq{TaskID: "task1", Text: string(big)}, &setResp); err != nil {
		t.Fatal(err)
	}
	if setResp.OK || setResp.Error == "" {
		t.Errorf("expected too-large error, got %+v", setResp)
	}
}

func waitForSocket(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", sockPath)
}

func TestDaemon_TransitionTaskOnExit(t *testing.T) {
	tests := []struct {
		name    string
		initial model.Status
		stopped bool
		want    model.Status
	}{
		{"in_progress + natural exit -> complete", model.StatusInProgress, false, model.StatusComplete},
		{"in_progress + stopped -> in_review", model.StatusInProgress, true, model.StatusInReview},
		{"in_review + exit -> in_review (no-op)", model.StatusInReview, false, model.StatusInReview},
		{"complete + exit -> complete (no-op)", model.StatusComplete, true, model.StatusComplete},
		{"pending + exit -> pending (no-op)", model.StatusPending, false, model.StatusPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			task := &model.Task{Name: "x", Status: tt.initial}
			testutil.NoError(t, d.db.Add(task))

			d.transitionTaskOnExit(task.ID, tt.stopped)

			got, err := d.db.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Status, tt.want)
		})
	}
}

func TestDaemon_TransitionTaskOnExit_MissingTask(t *testing.T) {
	d, _ := testDaemon(t)
	d.transitionTaskOnExit("nonexistent-id", false)
}

// TestDaemon_OnFinishFlipsStatus exercises the full path: a session started
// via the runner exits naturally, the runner's onFinish goroutine fires, and
// the DB row is flipped to Complete. This is the core fix for daemon-only
// setups (no TUI to flip status), so unit-testing transitionTaskOnExit alone
// isn't enough — we need to verify the wiring inside daemon.New() too.
func TestDaemon_OnFinishFlipsStatus(t *testing.T) {
	d, _ := testDaemon(t)
	if err := d.db.SetBackend("test", config.Backend{Command: "true"}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "exit-test", Status: model.StatusInProgress, Worktree: t.TempDir(), Backend: "test"}
	testutil.NoError(t, d.db.Add(task))

	if _, err := d.runner.Start(task, d.db.Config(), 24, 80, false); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fresh, _ := d.db.Get(task.ID)
		if fresh != nil && fresh.Status != model.StatusInProgress {
			testutil.Equal(t, fresh.Status, model.StatusComplete)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status flip; row stuck at %s", model.StatusInProgress)
}
