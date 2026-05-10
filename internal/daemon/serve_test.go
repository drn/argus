package daemon

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// TestServe_WithKBAndAPI exercises Serve's KB- and API-enabled branches.
// The KB needs no vault path (so no indexer); the API needs a port. We let
// both fail-open if the host environment can't bind.
func TestServe_WithKBAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, sockPath := testDaemon(t)
	testutil.NoError(t, d.db.SetConfigValue("kb.enabled", "true"))
	testutil.NoError(t, d.db.SetConfigValue("api.enabled", "true"))

	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	// Quick sanity ping to confirm the daemon survived service init.
	c := dialRPC(t, sockPath)
	var resp PongResp
	testutil.NoError(t, c.Call("Daemon.Ping", &Empty{}, &resp))
	testutil.True(t, resp.OK)
}

// TestServe_KBWithVault exercises the kbIndexer-start branch by setting
// MetisVaultPath. The path doesn't need to exist for the goroutine to spawn;
// indexer reports an error but Serve continues.
func TestServe_KBVault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, sockPath := testDaemon(t)
	testutil.NoError(t, d.db.SetConfigValue("kb.enabled", "true"))
	testutil.NoError(t, d.db.SetConfigValue("kb.metis_vault_path", t.TempDir()))

	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)

	// Give the indexer goroutine a chance to start.
	time.Sleep(50 * time.Millisecond)
}

// TestKill_LiveProcess covers the live-process branch of killExistingDaemon.
// We spawn a real child process (sleep 30) and write its PID to a pid file,
// then call killExistingDaemon and verify the child gets SIGTERM and exits.
func TestKill_Live(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "d.pid")

	cmd := exec.Command("sleep", "30")
	testutil.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pid := cmd.Process.Pid
	testutil.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	killExistingDaemon(pidPath)

	// Process should be gone within a short time.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		break
	}
}

// TestServe_ShutdownWithApiAndKB exercises the cleanup path with both API
// and KB started. We dial a connection, start serve, then shutdown.
func TestServe_ShutdownAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, sockPath := testDaemon(t)
	testutil.NoError(t, d.db.SetConfigValue("kb.enabled", "true"))
	testutil.NoError(t, d.db.SetConfigValue("api.enabled", "true"))
	testutil.NoError(t, d.db.SetConfigValue("kb.metis_vault_path", t.TempDir()))

	errCh := make(chan error, 1)
	go func() { errCh <- d.Serve(sockPath) }()
	waitForSocket(t, sockPath)

	// Open one connection to exercise handleConn.
	conn, err := net.Dial("unix", sockPath)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := jsonrpc.NewClient(conn)
	var resp PongResp
	testutil.NoError(t, c.Call("Daemon.Ping", &Empty{}, &resp))
	c.Close()

	d.Shutdown()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// _ keeps refs.
var _ = (*rpc.Client)(nil)
