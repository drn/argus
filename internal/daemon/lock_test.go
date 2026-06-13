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

// TestSingletonPathsForSock pins the trio derivation for both the daemon and a
// second singleton (the session-supervisor, P1): swapping the ".sock" suffix
// yields sibling ".pid" and ".lock" paths. The daemon row must be byte-identical
// to the prior hard-wired derivation (daemon.pid / daemon.lock).
func TestSingletonPathsForSock(t *testing.T) {
	tests := []struct {
		name           string
		sock           string
		wantPid, wantL string
	}{
		{"daemon", "/d/daemon.sock", "/d/daemon.pid", "/d/daemon.lock"},
		{"supervisor", "/d/supervisor.sock", "/d/supervisor.pid", "/d/supervisor.lock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := singletonPathsForSock(tt.sock)
			testutil.Equal(t, sp.sock, tt.sock)
			testutil.Equal(t, sp.pid, tt.wantPid)
			testutil.Equal(t, sp.lock, tt.wantL)
		})
	}
}

// TestSingletonPaths_IndependentLocks proves two singletons can coexist: the
// daemon's lock and the supervisor's lock are distinct files, so both acquire
// at once. This is what lets the session-supervisor (P1) run alongside the
// daemon without either's singleton guard rejecting the other — while a second
// acquire of the SAME lock still reports already-running.
func TestSingletonPaths_IndependentLocks(t *testing.T) {
	dir := t.TempDir()
	dsp := singletonPathsForSock(filepath.Join(dir, "daemon.sock"))
	ssp := singletonPathsForSock(filepath.Join(dir, "supervisor.sock"))

	// Distinct lock files.
	if dsp.lock == ssp.lock {
		t.Fatalf("daemon and supervisor share a lock path: %q", dsp.lock)
	}

	// Both acquire simultaneously — independent singletons.
	dl, err := acquireSingletonLock(dsp.lock, 50*time.Millisecond)
	testutil.NoError(t, err)
	t.Cleanup(func() { dl.Close() }) //nolint:errcheck
	sl, err := acquireSingletonLock(ssp.lock, 50*time.Millisecond)
	testutil.NoError(t, err)
	t.Cleanup(func() { sl.Close() }) //nolint:errcheck

	// A second acquire of the supervisor's own lock still contends.
	dup, err := acquireSingletonLock(ssp.lock, 50*time.Millisecond)
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("expected ErrDaemonAlreadyRunning on same-lock re-acquire, got %v", err)
	}
	if dup != nil {
		t.Fatal("expected nil handle on contention")
	}
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
