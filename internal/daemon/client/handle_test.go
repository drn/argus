package client

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// startedClient spins up a daemon (via testSetup) plus a connected client and
// a started long-running session. Returns the client and session handle.
// Caller doesn't need to clean up — testSetup registers cleanup.
func startedClient(t *testing.T, taskID, cmd string) (*Client, *RemoteSession) {
	t.Helper()
	_, sockPath, db := testSetup(t)
	if cmd != "" {
		testutil.NoError(t, db.SetBackend("test", config.Backend{Command: cmd}))
	}
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	task := &model.Task{ID: taskID, Name: "h", Backend: "test", Worktree: t.TempDir()}
	h, err := c.Start(task, config.Config{}, 24, 80, false)
	testutil.NoError(t, err)
	rs := h.(*RemoteSession)
	return c, rs
}

func TestH_PidAlive(t *testing.T) {
	_, rs := startedClient(t, "t-h-pid", "sleep 60")
	if rs.PID() == 0 {
		t.Errorf("expected non-zero PID, got 0")
	}
	testutil.True(t, rs.Alive())
}

func TestH_Done(t *testing.T) {
	_, rs := startedClient(t, "t-h-done", "sleep 60")
	select {
	case <-rs.Done():
		t.Fatal("Done channel closed prematurely")
	default:
	}
	testutil.Nil(t, rs.Err())
}

func TestH_PTYSize(t *testing.T) {
	_, rs := startedClient(t, "t-h-pty", "sleep 60")
	cols, rows := rs.PTYSize()
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)

	initCols, initRows := rs.InitialPTYSize()
	testutil.Equal(t, initCols, 80)
	testutil.Equal(t, initRows, 24)
}

func TestH_WorkDir(t *testing.T) {
	_, rs := startedClient(t, "t-h-wd", "sleep 60")
	wd := rs.WorkDir()
	// WorkDir on session is the cmd.Dir set by BuildCmd from task.Worktree.
	if wd == "" {
		t.Errorf("expected non-empty WorkDir, got empty")
	}
}

func TestH_IsIdle(t *testing.T) {
	_, rs := startedClient(t, "t-h-idle", "sleep 60")
	// Just call it — value depends on session timing.
	_ = rs.IsIdle()
}

func TestH_Resize(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		_, rs := startedClient(t, "t-h-rsz", "sleep 60")
		testutil.NoError(t, rs.Resize(40, 120))
	})
	t.Run("not found", func(t *testing.T) {
		_, sockPath, _ := testSetup(t)
		c, err := Connect(sockPath)
		testutil.NoError(t, err)
		t.Cleanup(func() { c.Close() })

		rs := newRemoteSession("ghost", c)
		err = rs.Resize(30, 100)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "session not found")
	})
}

func TestH_WriteIn(t *testing.T) {
	_, rs := startedClient(t, "t-h-wr", "sleep 60")
	zero := rs.LastInput()
	testutil.True(t, zero.IsZero())

	n, err := rs.WriteInput([]byte("hello"))
	testutil.NoError(t, err)
	testutil.Equal(t, n, 5)

	// LastInput should advance.
	got := rs.LastInput()
	testutil.False(t, got.IsZero())

	// Allow inputLoop time to flush the RPC so the goroutine exits cleanly later.
	time.Sleep(50 * time.Millisecond)
}

func TestH_WriteAfterClose(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	rs := newRemoteSession("dead", c)
	rs.close()

	// First WriteInput hits the closed branch — fills the channel buffer
	// first or selects done. With buffer of 64, the first write wins on
	// inputCh, but inputLoop already returned, so we close again to force
	// the done-path.
	for i := 0; i < 65; i++ {
		_, _ = rs.WriteInput([]byte("x"))
	}
	// Eventually one of the calls hits the done path and returns an error.
	// Acceptance is implicit (no panic, no deadlock).
}

func TestH_OutputTotal(t *testing.T) {
	_, rs := startedClient(t, "t-h-out", "bash -c 'echo seed; sleep 60'")

	// Wait for a byte or two to land.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(rs.RecentOutput()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	out := rs.RecentOutput()
	testutil.True(t, len(out) > 0)

	tail := rs.RecentOutputTail(3)
	testutil.True(t, len(tail) <= 3)

	tail2, total := rs.RecentOutputTailWithTotal(2)
	testutil.True(t, len(tail2) <= 2)
	if total == 0 {
		t.Errorf("expected total > 0, got 0")
	}

	tw := rs.TotalWritten()
	if tw == 0 {
		t.Errorf("expected TotalWritten > 0, got 0")
	}
}

func TestH_Stop(t *testing.T) {
	_, rs := startedClient(t, "t-h-stop", "sleep 60")
	testutil.NoError(t, rs.Stop())

	// After stop, the daemon's session should be gone shortly.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !rs.Alive() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestH_AddRemNoop(t *testing.T) {
	_, rs := startedClient(t, "t-h-aw", "sleep 60")
	// All writer methods are no-ops on RemoteSession — just call them.
	rs.AddWriter(nil)
	rs.AddWriterFrom(nil, 0)
	rs.RemoveWriter(nil)
}

func TestH_RefreshErr(t *testing.T) {
	// Simulate daemon-down by closing the rpc connection.
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	c.rpc.Close()

	rs := newRemoteSession("ghost", c)
	// Should not panic; refreshInfo silently swallows the error.
	rs.refreshInfo()
}

func TestH_UpdateInfo(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	rs := newRemoteSession("uinfo", c)

	// Direct method call.
	info := struct {
		Cols int
		Rows int
		PID  int
	}{}
	_ = info
	rs.updateInfo(rs.info) // call against a zero info
	testutil.Equal(t, rs.PID(), 0)
}

// TestClient_StopRPC covers the Stop method on the client (separate from
// RemoteSession.Stop, which is also covered above).
func TestC_StopRPC(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, sockPath, _ := testSetup(t)
		c, err := Connect(sockPath)
		testutil.NoError(t, err)
		t.Cleanup(func() { c.Close() })

		err = c.Stop("no-such")
		testutil.Error(t, err)
	})
	t.Run("running", func(t *testing.T) {
		c, rs := startedClient(t, "t-h-cstop", "sleep 60")
		testutil.NoError(t, c.Stop(rs.taskID))
	})
}

// TestClient_Shutdown covers the Shutdown RPC wrapper.
func TestC_Shutdown(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	testutil.NoError(t, c.Shutdown())
}

// TestClient_ClipboardGetSetClear covers Clipboard methods on the client.
func TestC_ClipGSC(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	// Initially empty.
	got, ok := c.ClipboardGet("t1")
	testutil.False(t, ok)
	testutil.Equal(t, got, "")

	// Stage via the daemon side directly to avoid needing ClipboardSet method
	// on the client (it's used by tests to seed the clipboard).
	// Use callWithTimeout via the underlying call() against ClipboardSet.
	// Easier: just go via the daemon.ClipboardSet RPC manually.
	type setReq struct {
		TaskID string
		Text   string
	}
	type stat struct {
		OK    bool
		Error string
	}
	var s stat
	testutil.NoError(t, c.rpc.Call("Daemon.ClipboardSet", &setReq{TaskID: "t1", Text: "v"}, &s))
	testutil.True(t, s.OK)

	got, ok = c.ClipboardGet("t1")
	testutil.True(t, ok)
	testutil.Equal(t, got, "v")

	testutil.NoError(t, c.ClipboardClear("t1"))

	// Confirm cleared via ClipboardGet.
	got, ok = c.ClipboardGet("t1")
	testutil.False(t, ok)
}

// TestClient_ClipboardGet_RPCErr covers the RPC-failure branch.
func TestC_ClipErr(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	c.rpc.Close()
	got, ok := c.ClipboardGet("t1")
	testutil.False(t, ok)
	testutil.Equal(t, got, "")
	// ClipboardClear should also propagate the error.
	testutil.Error(t, c.ClipboardClear("t1"))
	// Stop should propagate.
	testutil.Error(t, c.Stop("t1"))
	// Shutdown should propagate.
	testutil.Error(t, c.Shutdown())
}

// TestClient_RunningAndIdle covers the merged-RPC variant.
func TestC_RunIdle(t *testing.T) {
	c, rs := startedClient(t, "t-h-ri", "sleep 60")
	running, idle := c.RunningAndIdle()
	found := false
	for _, id := range running {
		if id == rs.taskID {
			found = true
		}
	}
	testutil.True(t, found)
	_ = idle
}

// TestClient_RunningIdle_Err covers the error branches.
func TestC_RunIdleErr(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	c.rpc.Close()
	running, idle := c.RunningAndIdle()
	testutil.Nil(t, running)
	testutil.Nil(t, idle)
}

// TestClient_WorkDir covers the client-level WorkDir lookup.
func TestC_WorkDir(t *testing.T) {
	c, rs := startedClient(t, "t-h-cwd", "sleep 60")
	wd := c.WorkDir(rs.taskID)
	if wd == "" {
		t.Errorf("expected non-empty WorkDir")
	}

	// Closing the rpc forces the error branch — empty string returned.
	c.rpc.Close()
	testutil.Equal(t, c.WorkDir(rs.taskID), "")
}

// TestClient_BootInfo covers BootInfo wrapper.
func TestC_BootInfo(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	info, err := c.BootInfo()
	testutil.NoError(t, err)
	if info.BinaryPath == "" {
		t.Logf("BinaryPath empty (test binary may be unresolved): %+v", info)
	}

	// Error branch — close rpc.
	c.rpc.Close()
	_, err = c.BootInfo()
	testutil.Error(t, err)
}

// TestC_WaitDownMissing covers the helper's missing-path branch.
func TestC_WaitDownMiss(t *testing.T) {
	WaitForShutdown(t.TempDir()+"/no-such.sock", 200*time.Millisecond)
}

// TestC_WaitDownTimeout creates a real file (not a daemon) at a path so the
// helper polls until timeout — exercises the loop body.
func TestC_WaitDownTO(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/x.sock"
	// Write a regular file so os.Stat succeeds and IsNotExist is false.
	testutil.NoError(t, writeFile(sock, "x"))

	start := time.Now()
	WaitForShutdown(sock, 200*time.Millisecond)
	dur := time.Since(start)
	if dur < 150*time.Millisecond {
		t.Errorf("WaitForShutdown returned too quickly: %v", dur)
	}
}

// TestClient_UpdateSelf covers the long-timeout RPC wrapper. The daemon's
// UpdateSelf returns an error when SourcePath is empty, which we pipe through
// to verify the wrapper propagates the resp.Error.
func TestC_UpdateSelf(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	out, err := c.UpdateSelf()
	// SourcePath isn't set — selfupdate returns an error. The wrapper should
	// surface it.
	testutil.Error(t, err)
	_ = out
}

// TestClient_UpdateSelf_Err covers the RPC-failure branch.
func TestC_UpdateErr(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	c.rpc.Close()
	_, err = c.UpdateSelf()
	testutil.Error(t, err)
}

// TestHandle_RecentOutputTailEmpty covers the read paths when no bytes have
// arrived yet.
func TestH_TailEmpty(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	rs := newRemoteSession("blank", c)
	testutil.Equal(t, len(rs.RecentOutput()), 0)
	testutil.Equal(t, len(rs.RecentOutputTail(10)), 0)
	tail, total := rs.RecentOutputTailWithTotal(10)
	testutil.Equal(t, len(tail), 0)
	testutil.Equal(t, total, uint64(0))
	testutil.Equal(t, rs.TotalWritten(), uint64(0))
}

// TestHandle_StopErr covers Stop error pass-through.
func TestH_StopErr(t *testing.T) {
	_, sockPath, _ := testSetup(t)
	c, err := Connect(sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	rs := newRemoteSession("ghost", c)
	err = rs.Stop()
	testutil.Error(t, err)
}

// writeFile is a shorthand for os.WriteFile with 0644.
func writeFile(p, content string) error {
	return os.WriteFile(p, []byte(content), 0o644)
}

// _ keeps test infra references stable.
var _ = strings.HasPrefix
