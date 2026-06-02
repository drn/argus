package daemon

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestDaemonLockPath(t *testing.T) {
	got := daemonLockPath("/some/dir/daemon.sock")
	testutil.Equal(t, got, "/some/dir/daemon.lock")
}

// TestAcquireSingletonLock_Contention is the core of the split-brain fix:
// while one holder has the lock, a second acquire must fail (so the losing
// daemon exits instead of binding the socket). After the holder releases,
// a fresh acquire must succeed.
func TestAcquireSingletonLock_Contention(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")

	first, err := acquireSingletonLock(lockPath, 50*time.Millisecond)
	testutil.NoError(t, err)
	if first == nil {
		t.Fatal("expected a lock file handle")
	}

	// Second acquire while the first is held must report already-running.
	second, err := acquireSingletonLock(lockPath, 50*time.Millisecond)
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("expected ErrDaemonAlreadyRunning, got %v", err)
	}
	if second != nil {
		t.Fatal("expected nil handle on contention")
	}

	// Release and re-acquire.
	testutil.NoError(t, first.Close())
	third, err := acquireSingletonLock(lockPath, time.Second)
	testutil.NoError(t, err)
	if third == nil {
		t.Fatal("expected to re-acquire after release")
	}
	testutil.NoError(t, third.Close())
}

// TestAcquireSingletonLock_OpenError surfaces a real (non-contention) error
// when the lock file can't be created — e.g. its parent dir doesn't exist.
func TestAcquireSingletonLock_OpenError(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "no-such-dir", "daemon.lock")
	f, err := acquireSingletonLock(lockPath, 50*time.Millisecond)
	testutil.Error(t, err)
	if errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatal("open error should not be reported as already-running")
	}
	if f != nil {
		t.Fatal("expected nil handle on open error")
	}
}

// TestServe_LockHeldReturnsAlreadyRunning verifies the singleton guard is wired
// into Serve: when another process already holds the lock, Serve returns
// ErrDaemonAlreadyRunning (the caller exits 0) without binding the socket.
func TestServe_LockHeldReturnsAlreadyRunning(t *testing.T) {
	d, sockPath := testDaemon(t)

	// Pre-hold the lock to simulate a daemon that won the race.
	held, err := acquireSingletonLock(daemonLockPath(sockPath), 50*time.Millisecond)
	testutil.NoError(t, err)
	t.Cleanup(func() { held.Close() }) //nolint:errcheck

	// Shrink the retry window so the test doesn't wait the full default.
	orig := daemonLockTimeout
	daemonLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { daemonLockTimeout = orig })

	err = d.Serve(sockPath)
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("expected ErrDaemonAlreadyRunning, got %v", err)
	}

	// ready must be closed so Shutdown waiters never block.
	select {
	case <-d.ready:
	case <-time.After(time.Second):
		t.Fatal("Serve did not close ready channel on lock contention")
	}
}
