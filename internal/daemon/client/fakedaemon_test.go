package client

import (
	"encoding/json"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

// fakeDaemon is a minimal in-process daemon-like server for stream tests.
// It exposes a settable SessionStatus reply, accepts stream connections,
// and can be made to drop the stream connection on demand.
type fakeDaemon struct {
	mu        sync.Mutex
	sock      string
	ln        net.Listener
	alive     bool   // SessionStatus.Alive value
	dropAfter int    // how many bytes to send before dropping the stream
	streamMsg []byte // bytes to send on stream before drop
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "f.sock")
	ln, err := net.Listen("unix", sock)
	testutil.NoError(t, err)
	fd := &fakeDaemon{sock: sock, ln: ln, alive: true}
	t.Cleanup(func() { ln.Close() })
	go fd.serve()
	return fd
}

// FakeRPCService is the registered RPC type — its handlers reach back into
// the fakeDaemon for state.
type FakeRPCService struct{ fd *fakeDaemon }

func (s *FakeRPCService) SessionStatus(req *daemon.TaskIDReq, resp *daemon.SessionInfo) error {
	s.fd.mu.Lock()
	defer s.fd.mu.Unlock()
	resp.TaskID = req.TaskID
	resp.Alive = s.fd.alive
	return nil
}

func (s *FakeRPCService) Ping(_ *daemon.Empty, resp *daemon.PongResp) error {
	resp.OK = true
	return nil
}

// Shutdown returns a daemon-side error so the client wrapper hits the
// resp.Error branch.
func (s *FakeRPCService) Shutdown(_ *daemon.Empty, resp *daemon.StatusResp) error {
	resp.Error = "fake-shutdown-fail"
	return nil
}

// ClipboardClear returns a daemon-side error so the client wrapper exercises
// its resp.Error branch.
func (s *FakeRPCService) ClipboardClear(_ *daemon.ClipboardClearReq, resp *daemon.StatusResp) error {
	resp.Error = "fake-clear-fail"
	return nil
}

// UpdateSelf returns a daemon-side error AND output so the client wrapper
// exercises both branches.
func (s *FakeRPCService) UpdateSelf(_ *daemon.Empty, resp *daemon.UpdateSelfResp) error {
	resp.Output = "fake-out"
	resp.Error = "fake-update-fail"
	return nil
}

// StopSession returns a daemon-side error.
func (s *FakeRPCService) StopSession(_ *daemon.TaskIDReq, resp *daemon.StatusResp) error {
	resp.Error = "fake-stop-fail"
	return nil
}

// Resize returns a daemon-side error so the RemoteSession.Resize wrapper
// exercises its resp.Error branch.
func (s *FakeRPCService) Resize(_ *daemon.ResizeReq, resp *daemon.StatusResp) error {
	resp.Error = "fake-resize-fail"
	return nil
}

// ListSessions returns one alive + one idle entry so the alive/idle branch
// bodies in Running/Idle/RunningAndIdle iterate over real data.
func (s *FakeRPCService) ListSessions(_ *daemon.Empty, resp *daemon.ListResp) error {
	resp.Sessions = []daemon.SessionInfo{
		{TaskID: "x", Alive: true, Idle: false},
		{TaskID: "y", Alive: true, Idle: true},
	}
	return nil
}

func (fd *fakeDaemon) serve() {
	server := rpc.NewServer()
	server.RegisterName("Daemon", &FakeRPCService{fd: fd}) //nolint:errcheck

	for {
		conn, err := fd.ln.Accept()
		if err != nil {
			return
		}
		go fd.handle(conn, server)
	}
}

func (fd *fakeDaemon) handle(conn net.Conn, server *rpc.Server) {
	defer conn.Close()
	prefix := make([]byte, 1)
	if _, err := io.ReadFull(conn, prefix); err != nil {
		return
	}
	switch prefix[0] {
	case 'R':
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	case 'S':
		// Read the header, then send streamMsg, then drop.
		dec := json.NewDecoder(conn)
		var hdr daemon.StreamHeader
		_ = dec.Decode(&hdr)
		fd.mu.Lock()
		msg := fd.streamMsg
		fd.mu.Unlock()
		if len(msg) > 0 {
			conn.Write(msg) //nolint:errcheck
		}
		// Close the conn to drop the stream.
		conn.Close()
	}
}

// TestStream_DropWithAliveSession exercises connectStream's retry loop when
// streamOnce returns (false, false) — process still alive but stream dropped.
// After 3 retries the function falls through to removeSessionStreamLost.
func TestStream_RetryAlive(t *testing.T) {
	fd := newFakeDaemon(t)

	// Build a Client wired to the fake daemon.
	conn, err := net.Dial("unix", fd.sock)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: fd.sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		c.rpc.Close()
	})

	// Wire an exit-info callback to verify the StreamLost flag eventually fires.
	exitCh := make(chan daemon.ExitInfo, 1)
	c.OnSessionExit(func(_ string, info daemon.ExitInfo) {
		select {
		case exitCh <- info:
		default:
		}
	})

	rs := newRemoteSession("retry-alive", c)
	c.mu.Lock()
	c.sessions["retry-alive"] = rs
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		rs.connectStream(fd.sock)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("connectStream did not return")
	}

	select {
	case info := <-exitCh:
		testutil.True(t, info.StreamLost)
	case <-time.After(2 * time.Second):
		t.Fatal("expected StreamLost callback")
	}
}

// TestStream_AliveFalse exercises streamOnce when SessionStatus reports
// Alive=false after the stream drops — connectStream sees exited=true and
// fires removeSession.
func TestStream_ExitClean(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.alive = false
	fd.mu.Unlock()

	conn, err := net.Dial("unix", fd.sock)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: fd.sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		c.rpc.Close()
	})

	exitCh := make(chan daemon.ExitInfo, 1)
	c.OnSessionExit(func(_ string, info daemon.ExitInfo) {
		select {
		case exitCh <- info:
		default:
		}
	})

	rs := newRemoteSession("exit-clean", c)
	c.mu.Lock()
	c.sessions["exit-clean"] = rs
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		rs.connectStream(fd.sock)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connectStream did not return")
	}

	select {
	case info := <-exitCh:
		testutil.False(t, info.StreamLost) // process exit, not stream lost
	case <-time.After(2 * time.Second):
		t.Fatal("expected exit callback")
	}
}

// fakeClient builds a Client wired to fd's socket. Caller is responsible
// for calling t.Cleanup to close it.
func fakeClient(t *testing.T, fd *fakeDaemon) *Client {
	t.Helper()
	conn, err := net.Dial("unix", fd.sock)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: fd.sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		c.rpc.Close()
	})
	return c
}

// TestC_Shutdown_RespErr covers Shutdown when the daemon returns resp.Error.
func TestC_ShutdownErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	err := c.Shutdown()
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "fake-shutdown-fail")
}

// TestC_ClipClearErr covers ClipboardClear when resp.Error is non-empty.
func TestC_ClipClrErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	err := c.ClipboardClear("x")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "fake-clear-fail")
}

// TestC_UpdateSelfRespErr covers UpdateSelf when resp.Error is non-empty.
func TestC_USelfErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	out, err := c.UpdateSelf()
	testutil.Error(t, err)
	testutil.Equal(t, out, "fake-out")
}

// TestC_StopRespErr covers Stop when resp.Error is non-empty.
func TestC_StopRespErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	err := c.Stop("x")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "fake-stop-fail")
}

// TestC_ResizeRespErr covers RemoteSession.Resize when resp.Error is non-empty.
func TestC_ResizeRespErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	rs := newRemoteSession("x", c)
	err := rs.Resize(30, 100)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "fake-resize-fail")
}

// TestC_RunIdleAlive covers the alive/idle branch bodies in Running/Idle.
func TestC_RunIdleBoth(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)

	running := c.Running()
	testutil.Equal(t, len(running), 2)

	idle := c.Idle()
	testutil.Equal(t, len(idle), 1)

	r2, i2 := c.RunningAndIdle()
	testutil.Equal(t, len(r2), 2)
	testutil.Equal(t, len(i2), 1)
}

// TestStream_DoneDuringRetry covers the post-streamOnce select where
// rs.done is closed between iterations. We let streamOnce drop quickly,
// SessionStatus says Alive=true → not exited, daemonDown=false. Then we
// close rs.done from the test goroutine and the next loop iteration's
// initial-select check exits early via removeSessionStreamLost.
//
// Tricky: the inner select that we want to cover (line 62-71) runs AFTER
// streamOnce returns (false, false) and BEFORE the for-loop body's top-of-
// iteration select runs. We close rs.done concurrently with the spinning
// loop. The race is benign — either branch we hit covers more lines.
func TestStream_DoneRetry(t *testing.T) {
	fd := newFakeDaemon(t)
	// alive=true means streamOnce returns (false, false) → connectStream's
	// post-stream select fires.

	c := fakeClient(t, fd)
	c.OnSessionExit(func(string, daemon.ExitInfo) {})

	rs := newRemoteSession("done-retry", c)
	c.mu.Lock()
	c.sessions["done-retry"] = rs
	c.mu.Unlock()

	// Run connectStream while we close rs.done after a small delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		rs.close()
	}()

	done := make(chan struct{})
	go func() {
		rs.connectStream(fd.sock)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("connectStream did not return")
	}
}

// TestStream_DialFailButDaemonAlive covers stream.go:100 — dial fails AND
// isSessionAlive succeeds. The fake daemon answers SessionStatus with
// Alive=false so the dial-failure branch returns (true, false) → exited.
// Wait — that's the existing path. We need the !reachable branch (line 100):
// dial fails, then isSessionAlive RPC also fails.
//
// We achieve this by: stopping the fake daemon's listener (so net.Dial
// fails for the stream), and closing the rpc connection (so SessionStatus
// also fails) — both in one go.
func TestStream_DialAndRPCFail(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)

	// Stop the daemon listener entirely.
	fd.ln.Close()
	// Also close the rpc client so isSessionAlive fails.
	c.rpc.Close()

	rs := newRemoteSession("dialfail", c)
	exited, daemonDown := rs.streamOnce(fd.sock)
	testutil.False(t, exited)
	testutil.True(t, daemonDown)
}

// TestStream_BytesAndExit exercises the read loop body — we send a byte,
// then close. SessionStatus reports Alive=false → exit branch.
func TestStream_BytesExit(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.alive = false
	fd.streamMsg = []byte("hello-stream-payload")
	fd.mu.Unlock()

	conn, err := net.Dial("unix", fd.sock)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: fd.sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		c.rpc.Close()
	})
	c.OnSessionExit(func(string, daemon.ExitInfo) {})

	rs := newRemoteSession("bytes", c)
	c.mu.Lock()
	c.sessions["bytes"] = rs
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		rs.connectStream(fd.sock)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connectStream did not return")
	}

	// Verify the bytes landed in the local ring buffer.
	got := string(rs.RecentOutput())
	testutil.Contains(t, got, "hello-stream-payload")
}
