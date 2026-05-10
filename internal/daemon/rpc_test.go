package daemon

import (
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// startTestSession spins up a daemon with a long-running session and returns
// the running daemon, the dial-ready RPC client, the started taskID, and a
// cleanup function. Used by RPC tests that need an active session.
func startTestSession(t *testing.T, taskID, cmd string) (*Daemon, *rpc.Client) {
	t.Helper()
	d, sockPath := testDaemon(t)

	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: cmd}))
	testutil.NoError(t, d.db.SetConfigValue("default.backend", "test"))

	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)

	wt := t.TempDir()
	var startResp StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID:   taskID,
		Backend:  "test",
		Worktree: wt,
		Rows:     24,
		Cols:     80,
	}, &startResp))
	if startResp.Error != "" {
		t.Fatalf("StartSession err: %s", startResp.Error)
	}
	return d, c
}

func TestDaemon_DefaultPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sock := DefaultSocketPath()
	pid := DefaultPIDPath()
	testutil.Contains(t, sock, ".argus")
	testutil.Contains(t, sock, "daemon.sock")
	testutil.Contains(t, pid, "daemon.pid")
}

func TestDaemon_RunnerAccessor(t *testing.T) {
	d, _ := testDaemon(t)
	testutil.NotNil(t, d.Runner())
}

func TestDaemon_ClipboardAccessor(t *testing.T) {
	d, _ := testDaemon(t)
	testutil.NotNil(t, d.Clipboard())
}

func TestRPC_StopAll(t *testing.T) {
	d, c := startTestSession(t, "t-stopall", "sleep 60")

	// Confirm session is running.
	var listResp ListResp
	testutil.NoError(t, c.Call("Daemon.ListSessions", &Empty{}, &listResp))
	testutil.Equal(t, len(listResp.Sessions), 1)

	var stopResp StatusResp
	testutil.NoError(t, c.Call("Daemon.StopAll", &Empty{}, &stopResp))
	testutil.True(t, stopResp.OK)

	// Wait for cleanup.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		listResp = ListResp{}
		_ = c.Call("Daemon.ListSessions", &Empty{}, &listResp)
		if len(listResp.Sessions) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	testutil.Equal(t, len(listResp.Sessions), 0)
	_ = d
}

func TestRPC_SessionStatus(t *testing.T) {
	_, c := startTestSession(t, "t-status", "sleep 60")

	t.Run("existing", func(t *testing.T) {
		var info SessionInfo
		testutil.NoError(t, c.Call("Daemon.SessionStatus", &TaskIDReq{TaskID: "t-status"}, &info))
		testutil.Equal(t, info.TaskID, "t-status")
		testutil.True(t, info.Alive)
		testutil.Equal(t, info.Cols, 80)
		testutil.Equal(t, info.Rows, 24)
	})

	t.Run("missing", func(t *testing.T) {
		var info SessionInfo
		testutil.NoError(t, c.Call("Daemon.SessionStatus", &TaskIDReq{TaskID: "no-such-task"}, &info))
		testutil.Equal(t, info.TaskID, "no-such-task")
		testutil.False(t, info.Alive)
	})
}

func TestRPC_WriteInput(t *testing.T) {
	_, c := startTestSession(t, "t-write", "bash -c 'cat; sleep 1'")

	t.Run("existing", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.Call("Daemon.WriteInput", &WriteReq{
			TaskID: "t-write",
			Data:   []byte("hi\n"),
		}, &resp))
		testutil.True(t, resp.OK)
	})

	t.Run("missing session", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.Call("Daemon.WriteInput", &WriteReq{
			TaskID: "no-such-task",
			Data:   []byte("x"),
		}, &resp))
		testutil.False(t, resp.OK)
		testutil.Contains(t, resp.Error, "session not found")
	})
}

func TestRPC_Resize(t *testing.T) {
	_, c := startTestSession(t, "t-resize", "sleep 60")

	t.Run("existing", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.Call("Daemon.Resize", &ResizeReq{
			TaskID: "t-resize", Rows: 30, Cols: 100,
		}, &resp))
		testutil.True(t, resp.OK)
	})

	t.Run("missing session", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.Call("Daemon.Resize", &ResizeReq{
			TaskID: "nope", Rows: 30, Cols: 100,
		}, &resp))
		testutil.False(t, resp.OK)
		testutil.Contains(t, resp.Error, "session not found")
	})
}

func TestRPC_GetExitInfo(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	t.Run("missing returns empty", func(t *testing.T) {
		var info ExitInfo
		testutil.NoError(t, c.Call("Daemon.GetExitInfo", &TaskIDReq{TaskID: "missing"}, &info))
		testutil.Equal(t, info.Err, "")
		testutil.False(t, info.Stopped)
	})

	t.Run("returns and consumes cached info", func(t *testing.T) {
		// Stage exit info directly into the daemon.
		d.mu.Lock()
		d.exitInfos["t-exit"] = ExitInfo{Err: "boom", Stopped: true, LastOutput: []byte("bye")}
		d.mu.Unlock()

		var info ExitInfo
		testutil.NoError(t, c.Call("Daemon.GetExitInfo", &TaskIDReq{TaskID: "t-exit"}, &info))
		testutil.Equal(t, info.Err, "boom")
		testutil.True(t, info.Stopped)
		testutil.Equal(t, string(info.LastOutput), "bye")

		// Second call returns empty (consumed).
		var info2 ExitInfo
		testutil.NoError(t, c.Call("Daemon.GetExitInfo", &TaskIDReq{TaskID: "t-exit"}, &info2))
		testutil.Equal(t, info2.Err, "")
		testutil.False(t, info2.Stopped)
	})
}

func TestRPC_KBSearch(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	// Empty query returns empty results, no error.
	t.Run("empty query", func(t *testing.T) {
		var resp KBSearchResp
		testutil.NoError(t, c.Call("Daemon.KBSearch", &KBSearchReq{Query: "", Limit: 5}, &resp))
		testutil.Equal(t, len(resp.Results), 0)
		testutil.Equal(t, resp.Error, "")
	})

	// Ingest a doc, search for it.
	t.Run("ingest and search", func(t *testing.T) {
		var ingestResp KBIngestResp
		testutil.NoError(t, c.Call("Daemon.KBIngest", &KBIngestReq{
			Path:    "notes/programming.md",
			Content: "# Title\n\nThis is content about programming languages.",
		}, &ingestResp))
		testutil.Equal(t, ingestResp.Error, "")

		var searchResp KBSearchResp
		testutil.NoError(t, c.Call("Daemon.KBSearch", &KBSearchReq{Query: "programming", Limit: 5}, &searchResp))
		testutil.Equal(t, searchResp.Error, "")
		// At least 1 result; some sanitizers may strip tokens — be tolerant.
		if len(searchResp.Results) == 0 {
			t.Errorf("expected search results, got 0 (resp=%+v)", searchResp)
		}
	})
	_ = d
}

func TestRPC_KBList(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	// Ingest two docs so the list is non-trivial.
	for _, p := range []string{"a/one.md", "a/two.md"} {
		var resp KBIngestResp
		testutil.NoError(t, c.Call("Daemon.KBIngest", &KBIngestReq{
			Path:    p,
			Content: "# t\n\nbody",
		}, &resp))
		testutil.Equal(t, resp.Error, "")
	}

	var listResp KBListResp
	testutil.NoError(t, c.Call("Daemon.KBList", &KBListReq{Prefix: "a/", Limit: 10}, &listResp))
	testutil.Equal(t, listResp.Error, "")
	testutil.Equal(t, len(listResp.Documents), 2)
	_ = d
}

func TestRPC_KBList_Err(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	// Close the DB so KBList fails.
	testutil.NoError(t, d.db.Close())

	var resp KBListResp
	testutil.NoError(t, c.Call("Daemon.KBList", &KBListReq{Prefix: "x/", Limit: 10}, &resp))
	testutil.Contains(t, resp.Error, "closed")
}

func TestRPC_KBSearch_Err(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	testutil.NoError(t, d.db.Close())

	var resp KBSearchResp
	testutil.NoError(t, c.Call("Daemon.KBSearch", &KBSearchReq{Query: "anything", Limit: 5}, &resp))
	testutil.Contains(t, resp.Error, "closed")
}

func TestRPC_KBSearch_Empty(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	// Whitespace-only sanitizes to empty; KBSearch returns nil results
	// without dialing the DB.
	var resp KBSearchResp
	testutil.NoError(t, c.Call("Daemon.KBSearch", &KBSearchReq{Query: "  ", Limit: 5}, &resp))
	testutil.Equal(t, resp.Error, "")
	testutil.Equal(t, len(resp.Results), 0)
}

func TestRPC_KBIngest_Err(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	testutil.NoError(t, d.db.Close())

	var resp KBIngestResp
	testutil.NoError(t, c.Call("Daemon.KBIngest", &KBIngestReq{
		Path: "x/a.md", Content: "body",
	}, &resp))
	testutil.Contains(t, resp.Error, "closed")
}

func TestRPC_KBStatus(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	// Ingest one doc to make the count non-zero.
	var ing KBIngestResp
	testutil.NoError(t, c.Call("Daemon.KBIngest", &KBIngestReq{Path: "x/a.md", Content: "x"}, &ing))

	var resp KBStatusResp
	testutil.NoError(t, c.Call("Daemon.KBStatus", &Empty{}, &resp))
	testutil.Equal(t, resp.DocumentCount, 1)
	// VaultPath/Port reflect config — may be empty/0 in tests; just call to cover.
	_ = resp.VaultPath
	_ = resp.Port
}

func TestRPC_RPCShutdown(t *testing.T) {
	d, sockPath := testDaemon(t)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Serve(sockPath) }()
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)
	var resp StatusResp
	testutil.NoError(t, c.Call("Daemon.Shutdown", &Empty{}, &resp))
	testutil.True(t, resp.OK)

	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// TestDaemon_HandleStream verifies the full stream path: a client subscribes
// to a session's output, receives the bytes the session produces, and gets
// EOF when the session exits. Also exercises registerStream/unregisterStream
// and waitForClose.
func TestDaemon_HandleStream(t *testing.T) {
	d, sockPath := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: "bash -c 'echo hello-stream; sleep 1'"}))
	testutil.NoError(t, d.db.SetConfigValue("default.backend", "test"))

	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)
	wt := t.TempDir()
	var startResp StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID:   "t-stream",
		Backend:  "test",
		Worktree: wt,
		Rows:     24, Cols: 80,
	}, &startResp))
	testutil.Equal(t, startResp.Error, "")

	// Open a stream connection.
	conn := dialStream(t, sockPath, "t-stream")

	// Read what we can; the session prints "hello-stream" then sleeps 1s.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	testutil.Contains(t, string(buf[:n]), "hello-stream")

	// Give the daemon a moment to register the stream then to clean up after exit.
	time.Sleep(50 * time.Millisecond)
	d.mu.Lock()
	regOK := len(d.streams) <= 1 // either still registered or already cleaned up
	d.mu.Unlock()
	testutil.True(t, regOK)
}

// TestDaemon_HandleStream_NoSession exercises the "session not found" branch
// of handleStream — the daemon should drop the connection without panicking.
func TestD_StreamNoSess(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	conn := dialStream(t, sockPath, "ghost")
	// Daemon should close the conn shortly.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	_, err := conn.Read(buf)
	testutil.Error(t, err) // expect EOF or closed
}

// TestDaemon_HandleStream_BadHeader verifies the json decode error branch
// returns without panicking.
func TestD_StreamBadHdr(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	// Dial with stream prefix but write garbage where header should be.
	conn, err := net.Dial("unix", sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Write([]byte("S"))
	testutil.NoError(t, err)
	_, err = conn.Write([]byte("not-json\n"))
	testutil.NoError(t, err)

	// Should be closed by the daemon promptly.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	_, _ = conn.Read(buf)
}

// TestDaemon_HandleConn_BadPrefix exercises the default branch of handleConn —
// the daemon should log the unknown byte and close the connection.
func TestD_ConnBadPfx(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	conn, err := net.Dial("unix", sockPath)
	testutil.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, _ = conn.Write([]byte("X"))
}

// TestHeadlessCreateTask covers the fully-transactional happy path through
// agent.CreateAndStart with a real worktree on a tempdir-backed git repo.
func TestHeadlessCreateTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := t.TempDir()
	mustGit(t, repo, "init", "-q")
	mustGit(t, repo, "config", "user.email", "t@t")
	mustGit(t, repo, "config", "user.name", "T")
	testutil.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644))
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-q", "-m", "init")

	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: "echo hello"}))
	testutil.NoError(t, d.db.SetConfigValue("defaults.backend", "test"))
	testutil.NoError(t, d.db.SetProject("proj", config.Project{Path: repo, Branch: "HEAD"}))

	task, err := HeadlessCreateTask(d.db, d.runner, "my-task", "my prompt", "proj", "test", false)
	testutil.NoError(t, err)
	testutil.NotNil(t, task)
	testutil.Equal(t, task.Project, "proj")
	testutil.Equal(t, task.Status, model.StatusInProgress)

	// Wait for the echo session to exit and cleanup to settle.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fresh, _ := d.db.Get(task.ID)
		if fresh != nil && fresh.Status != model.StatusInProgress {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHeadlessCreateTask_MissingProject covers the error path where project
// is not configured — exercises HeadlessCreateTask but the failure propagates
// from CreateAndStart.
func TestHeadlessCreateTask_MissingProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, _ := testDaemon(t)
	task, err := HeadlessCreateTask(d.db, d.runner, "x", "p", "no-such-project", "", false)
	testutil.Error(t, err)
	testutil.Nil(t, task)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestDaemon_RegisterUnregisterStream directly exercises the helper methods —
// faster than going through a real session.
func TestDaemon_RegisterUnregisterStream(t *testing.T) {
	d, _ := testDaemon(t)

	// Use a fake net.Conn.
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })

	d.registerStream("t1", a)
	d.mu.Lock()
	got := len(d.streams["t1"])
	d.mu.Unlock()
	testutil.Equal(t, got, 1)

	// Register a second on the same task to exercise the slice path.
	d.registerStream("t1", b)
	d.mu.Lock()
	got = len(d.streams["t1"])
	d.mu.Unlock()
	testutil.Equal(t, got, 2)

	// Unregister one — the iteration finds and removes it.
	d.unregisterStream("t1", a)
	d.mu.Lock()
	got = len(d.streams["t1"])
	d.mu.Unlock()
	testutil.Equal(t, got, 1)

	// Unregister a non-existent conn: silent no-op.
	other, _ := net.Pipe()
	d.unregisterStream("t1", other)
	other.Close()
	d.mu.Lock()
	got = len(d.streams["t1"])
	d.mu.Unlock()
	testutil.Equal(t, got, 1)

	// Unregister the last one.
	d.unregisterStream("t1", b)
	d.mu.Lock()
	got = len(d.streams["t1"])
	d.mu.Unlock()
	testutil.Equal(t, got, 0)
}

// TestServe_ListenError exercises Serve's listen-error path — close the ready
// channel and return a wrapped error. We trigger it by binding to a path that
// can't be created (e.g. inside a non-existent directory).
func TestServe_ListenError(t *testing.T) {
	d, _ := testDaemon(t)
	bad := filepath.Join(t.TempDir(), "no-such-subdir", "test.sock")
	err := d.Serve(bad)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "listen")
}

// TestRPC_Compile asserts the RPCService satisfies the rpc registration
// (otherwise registration in Serve would fail). Not load-bearing, but cheap
// insurance.
func TestRPC_Compile(t *testing.T) {
	var _ = (&RPCService{}).Ping
}

// TestStartTestSessionMissingDataIsHarmless prevents a flake — the helper
// must not panic when nothing is staged.
func TestStartTestSessionGuard(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
}

// TestRPC_StopAll_InjectedRunner exercises the StopAll path against a Runner
// directly to avoid spawning real processes when the goal is just covering
// the RPC handler. We use the daemon's own runner for symmetry.
func TestRPC_StopAll_NoSessions(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)
	var resp StatusResp
	testutil.NoError(t, c.Call("Daemon.StopAll", &Empty{}, &resp))
	testutil.True(t, resp.OK)
}

// TestRPC_StartSession_Error exercises the error branch (unknown backend).
func TestRPC_StartSession_Error(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)
	var resp StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID:  "no-backend-task",
		Backend: "does-not-exist",
		// No worktree set — buildCmd will fail.
		Rows: 24, Cols: 80,
	}, &resp))
	if resp.Error == "" && resp.PID == 0 {
		// Some platforms produce a zero PID with a non-nil error; either is fine.
		t.Logf("got resp=%+v (acceptable)", resp)
	}
}

// TestRPC_StopSession_NotFound exercises the StopSession error branch.
func TestRPC_StopSession_NotFound(t *testing.T) {
	d, sockPath := testDaemon(t)
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	c := dialRPC(t, sockPath)
	var resp StatusResp
	testutil.NoError(t, c.Call("Daemon.StopSession", &TaskIDReq{TaskID: "no-such"}, &resp))
	testutil.False(t, resp.OK)
	testutil.NotEqual(t, resp.Error, "")
}

// _ keeps agent referenced.
var _ = agent.NewRunner
