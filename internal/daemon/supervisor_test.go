package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// testSupervisor builds a Supervisor whose cfgFn returns the supplied config
// and a temp socket path under t.TempDir(). It NEVER touches the real
// supervisor/daemon socket. Short names keep the macOS 104-byte sun_path limit
// comfortable.
func testSupervisor(t *testing.T, cfg config.Config) (*Supervisor, string) {
	t.Helper()
	s := NewSupervisor(func() config.Config { return cfg })
	sockPath := filepath.Join(t.TempDir(), "s.sock")
	return s, sockPath
}

// supCfg is DefaultConfig plus a "test" backend bound to cmd, selected as the
// default. cmd is a shell-less program/args string resolved by agent.BuildCmd.
func supCfg(cmd string) config.Config {
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: cmd}
	cfg.Defaults.Backend = "test"
	return cfg
}

// pollExitInfo calls GetExitInfo until the predicate accepts the result or the
// deadline passes. GetExitInfo consumes once, so the first satisfying call wins.
func pollExitInfo(t *testing.T, c interface {
	Call(string, any, any) error
}, taskID string, ok func(ExitInfo) bool) ExitInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var info ExitInfo
		testutil.NoError(t, c.Call("Daemon.GetExitInfo", &TaskIDReq{TaskID: taskID}, &info))
		if ok(info) {
			return info
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("exit info for %q never satisfied predicate", taskID)
	return ExitInfo{}
}

// TestSup_PingHello verifies the supervisor answers the session-server Ping AND
// the version handshake under the "Daemon" service name (so the existing client
// speaks to it unchanged).
func TestSup_PingHello(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)

	var pong PongResp
	testutil.NoError(t, c.Call("Daemon.Ping", &Empty{}, &pong))
	testutil.True(t, pong.OK)

	var hello HelloResp
	testutil.NoError(t, c.Call("Daemon.Hello", &Empty{}, &hello))
	testutil.Equal(t, hello.ProtocolVersion, ProtocolVersion)
	if hello.BinaryPath == "" {
		t.Error("expected Hello.BinaryPath populated")
	}
	if hello.BootedAt.IsZero() {
		t.Error("expected Hello.BootedAt populated")
	}
}

// TestSup_StartListStop drives the core session RPCs over the supervisor socket
// with a long-lived fake (sleep), exactly as the daemon's StartAndStop test.
func TestSup_StartListStop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, supCfg("sleep 60"))
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)

	var sr StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID: "t1", Backend: "test", Worktree: t.TempDir(), Rows: 24, Cols: 80,
	}, &sr))
	testutil.Equal(t, sr.Error, "")
	if sr.PID == 0 {
		t.Error("expected non-zero PID")
	}

	var lr ListResp
	testutil.NoError(t, c.Call("Daemon.ListSessions", &Empty{}, &lr))
	testutil.Equal(t, len(lr.Sessions), 1)
	testutil.Equal(t, lr.Sessions[0].TaskID, "t1")

	var stop StatusResp
	testutil.NoError(t, c.Call("Daemon.StopSession", &TaskIDReq{TaskID: "t1"}, &stop))
	testutil.True(t, stop.OK)

	// Session should drain out of the list.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var lr2 ListResp
		testutil.NoError(t, c.Call("Daemon.ListSessions", &Empty{}, &lr2))
		if len(lr2.Sessions) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("session still listed after StopSession")
}

// TestSup_StreamBytes verifies the 'S' stream path: a subscriber receives the
// bytes the session produces. Proves handleStream + readLoop fan-out moved into
// the supervisor intact.
func TestSup_StreamBytes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, supCfg("bash -c 'echo hello-sup; sleep 1'"))
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var sr StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID: "ts", Backend: "test", Worktree: t.TempDir(), Rows: 24, Cols: 80,
	}, &sr))
	testutil.Equal(t, sr.Error, "")

	conn := dialStream(t, sock, "ts")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	testutil.Contains(t, string(buf[:n]), "hello-sup")
}

// TestSup_ExitCachingCleanExit proves the exit-caching-only onFinish caches a
// CLEAN exit (process exited 0): GetExitInfo returns it with the captured last
// output and CleanExit()==true. This is the #707-preservation seam — the
// supervisor is the always-parent so Cmd.Wait observes the real (nil) exit,
// and the daemon (P2) fetches this exact ExitInfo to drive the DB flip.
func TestSup_ExitCachingCleanExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, supCfg("bash -c 'echo done-clean'"))
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var sr StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID: "tc", Backend: "test", Worktree: t.TempDir(), Rows: 24, Cols: 80,
	}, &sr))
	testutil.Equal(t, sr.Error, "")

	// LastOutput non-empty is the "cached" signal (a clean exit's Err/Stopped
	// are zero, indistinguishable from the missing-entry zero value).
	info := pollExitInfo(t, c, "tc", func(e ExitInfo) bool { return len(e.LastOutput) > 0 })
	testutil.True(t, info.CleanExit())
	testutil.Equal(t, info.Err, "")
	testutil.False(t, info.Stopped)
	testutil.Contains(t, string(info.LastOutput), "done-clean")
}

// TestSup_ExitCachingCrash proves a NON-clean exit (process exited non-zero) is
// cached with a non-empty Err, so CleanExit()==false. The supervisor never
// interprets it — the daemon does — but the distinction must survive the cache.
func TestSup_ExitCachingCrash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, supCfg("bash -c 'exit 3'"))
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var sr StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID: "tx", Backend: "test", Worktree: t.TempDir(), Rows: 24, Cols: 80,
	}, &sr))
	testutil.Equal(t, sr.Error, "")

	info := pollExitInfo(t, c, "tx", func(e ExitInfo) bool { return e.Err != "" })
	testutil.False(t, info.CleanExit())
	testutil.False(t, info.Stopped)
}

// TestSup_ExitCachingStopped proves an explicit StopSession is cached with
// Stopped=true (CleanExit()==false), distinct from a self-exit.
func TestSup_ExitCachingStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, supCfg("sleep 60"))
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var sr StartResp
	testutil.NoError(t, c.Call("Daemon.StartSession", &StartReq{
		TaskID: "tp", Backend: "test", Worktree: t.TempDir(), Rows: 24, Cols: 80,
	}, &sr))
	testutil.Equal(t, sr.Error, "")

	var stop StatusResp
	testutil.NoError(t, c.Call("Daemon.StopSession", &TaskIDReq{TaskID: "tp"}, &stop))
	testutil.True(t, stop.OK)

	info := pollExitInfo(t, c, "tp", func(e ExitInfo) bool { return e.Stopped })
	testutil.False(t, info.CleanExit())
}

// TestSup_NilCfgFallsBackToDefault pins the runnable-binary guarantee: a nil
// cfgFn resolves to config.DefaultConfig so StartSession can still build a
// command. We assert the promoted core cfgFn directly (no serve needed).
func TestSup_NilCfgFallsBackToDefault(t *testing.T) {
	s := NewSupervisor(nil)
	cfg := s.cfgFn()
	testutil.Equal(t, cfg.Defaults.Backend, "claude")
	if _, ok := cfg.Backends["claude"]; !ok {
		t.Error("expected default 'claude' backend present")
	}
}

// TestSup_LockHeldAlreadyRunning verifies the supervisor's INDEPENDENT singleton
// guard is wired into Serve: with the supervisor lock pre-held, Serve returns
// ErrDaemonAlreadyRunning without binding, and closes ready so Shutdown waiters
// never block. (Trio-independence vs the daemon is pinned in lock_test.go.)
func TestSup_LockHeldAlreadyRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())

	held, err := acquireSingletonLock(daemonLockPath(sock), 50*time.Millisecond)
	testutil.NoError(t, err)
	t.Cleanup(func() { held.Close() }) //nolint:errcheck

	orig := daemonLockTimeout
	daemonLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { daemonLockTimeout = orig })

	err = s.Serve(sock)
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("expected ErrDaemonAlreadyRunning, got %v", err)
	}
	select {
	case <-s.ready:
	case <-time.After(time.Second):
		t.Fatal("Serve did not close ready on lock contention")
	}
}

// TestSup_CoexistsWithDaemon proves a daemon and a supervisor run side by side:
// distinct sockets, both answer Ping. This is what lets the supervisor host
// agents while the daemon bounces.
func TestSup_CoexistsWithDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, dsock := testDaemon(t)
	go d.Serve(dsock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, dsock)

	s, ssock := testSupervisor(t, config.DefaultConfig())
	go s.Serve(ssock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, ssock)

	dc := dialRPC(t, dsock)
	var dp PongResp
	testutil.NoError(t, dc.Call("Daemon.Ping", &Empty{}, &dp))
	testutil.True(t, dp.OK)

	sc := dialRPC(t, ssock)
	var sp PongResp
	testutil.NoError(t, sc.Call("Daemon.Ping", &Empty{}, &sp))
	testutil.True(t, sp.OK)

	// The supervisor exposes Hello; the daemon does not (distinct surface).
	var hello HelloResp
	testutil.NoError(t, sc.Call("Daemon.Hello", &Empty{}, &hello))
	testutil.Equal(t, hello.ProtocolVersion, ProtocolVersion)
}

// TestSup_GracefulShutdown verifies Serve returns nil on Shutdown and cleanup
// removes the socket + pid files and releases the flock (a later acquire of the
// same lock succeeds).
func TestSup_GracefulShutdown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())

	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(sock) }()
	waitForSocket(t, sock)

	sp := singletonPathsForSock(sock)

	s.Shutdown()
	select {
	case err := <-errCh:
		testutil.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket not removed on shutdown: %v", err)
	}
	if _, err := os.Stat(sp.pid); !os.IsNotExist(err) {
		t.Errorf("pid file not removed on shutdown: %v", err)
	}
	// Lock released — a fresh acquire succeeds.
	lk, err := acquireSingletonLock(sp.lock, time.Second)
	testutil.NoError(t, err)
	testutil.NoError(t, lk.Close())
}

// TestSup_ShutdownViaRPC verifies the Shutdown RPC (the stop subcommand's path)
// brings the serve loop down cleanly.
func TestSup_ShutdownViaRPC(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())

	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(sock) }()
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var resp StatusResp
	testutil.NoError(t, c.Call("Daemon.Shutdown", &Empty{}, &resp))
	testutil.True(t, resp.OK)

	select {
	case err := <-errCh:
		testutil.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown RPC")
	}
}

// TestSup_UnknownPrefixByte covers handleConn's default branch — an unknown
// first byte is logged and the conn closed without panicking.
func TestSup_UnknownPrefixByte(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	conn, err := net.Dial("unix", sock)
	testutil.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	_, err = conn.Write([]byte("Z"))
	testutil.NoError(t, err)
	// The supervisor closes the conn; a read returns EOF (no panic, no hang).
	conn.SetReadDeadline(time.Now().Add(time.Second)) //nolint:errcheck
	buf := make([]byte, 1)
	_, _ = conn.Read(buf)
}
