package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/drn/argus/internal/db"
)

// autoStartFork is the test-hostile body of AutoStart: it fork/execs the
// current binary as `argus daemon start` and polls for the socket. Under
// `go test`, doing so would re-run the entire test package as an orphaned
// child — the exact fork bomb ErrTestBinary in client.go is designed to
// prevent. This file is listed in coverage-ignore.txt because the only
// way to exercise it from tests is to break that invariant.
func autoStartFork(sockPath string) (*Client, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	// Create a symlink named "argusd" so Activity Monitor shows that name
	// instead of the generic binary name.
	daemonExe := filepath.Join(db.DataDir(), "argusd")
	target, _ := os.Readlink(daemonExe)
	if target != exe {
		os.Remove(daemonExe) //nolint:errcheck
		if err := os.Symlink(exe, daemonExe); err != nil {
			daemonExe = exe // fall back to original binary
		}
	}

	cmd := exec.Command(daemonExe, "daemon", "start") //nolint:gosec // daemonExe is os.Executable() / argusd symlink
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from parent process group so the daemon survives TUI exit.
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// Release the child process so it isn't reaped when we exit.
	cmd.Process.Release() //nolint:errcheck // detach-only; non-fatal if it fails

	// Poll for the socket to become available.
	const (
		pollInterval = 50 * time.Millisecond
		maxWait      = 3 * time.Second
	)
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		if client, err := Connect(sockPath); err == nil {
			return client, nil
		}
	}

	return nil, fmt.Errorf("daemon did not become ready within %s", maxWait)
}
