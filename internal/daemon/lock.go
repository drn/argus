package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrDaemonAlreadyRunning is returned by Serve when another daemon already
// holds the singleton lock. The caller should exit cleanly (status 0) rather
// than treat it as fatal — a healthy daemon is already serving the socket.
var ErrDaemonAlreadyRunning = errors.New("daemon already running")

// daemonLockTimeout bounds how long acquireSingletonLock retries a contended
// lock. It exists to absorb the brief gap between killExistingDaemon
// force-killing the prior daemon (SIGKILL is asynchronous) and the kernel
// releasing that process's flock. A var (not const) so tests can shrink it.
var daemonLockTimeout = 2 * time.Second

// daemonLockPath derives the lock-file path from the socket path so tests in
// temp dirs never touch the real ~/.argus/daemon.lock.
func daemonLockPath(sockPath string) string {
	return filepath.Join(filepath.Dir(sockPath), "daemon.lock")
}

// acquireSingletonLock takes an exclusive, non-blocking advisory lock on the
// daemon lock file. Exactly one process can hold it at a time, which is what
// guarantees exactly one daemon binds the socket — preventing the split-brain
// where a startup race lets multiple daemons each unlink+rebind the socket.
//
// The returned *os.File MUST be kept open for the daemon's lifetime; closing
// it (or process exit) releases the lock. Never remove the lock file —
// flock semantics depend on every contender opening the same inode.
//
// It retries with a short interval up to maxWait to absorb the kill→release
// gap, then returns ErrDaemonAlreadyRunning if the lock is still held.
func acquireSingletonLock(lockPath string, maxWait time.Duration) (*os.File, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	const retryInterval = 50 * time.Millisecond
	deadline := time.Now().Add(maxWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close() //nolint:errcheck // best-effort; the open fd is being discarded
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close() //nolint:errcheck // best-effort; the open fd is being discarded
			return nil, ErrDaemonAlreadyRunning
		}
		time.Sleep(retryInterval)
	}
}
