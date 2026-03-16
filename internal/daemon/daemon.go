package daemon

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
)

// DefaultSocketPath returns the default Unix socket path.
func DefaultSocketPath() string {
	return filepath.Join(db.DataDir(), "daemon.sock")
}

// DefaultPIDPath returns the default PID file path.
func DefaultPIDPath() string {
	return filepath.Join(db.DataDir(), "daemon.pid")
}

// ExitInfo holds the exit state of a finished session, cached briefly
// so clients can query it after the stream closes.
type ExitInfo struct {
	Err        string
	Stopped    bool
	LastOutput []byte
}

// Daemon manages agent sessions and exposes them over a Unix socket.
type Daemon struct {
	db        *db.DB
	runner    *agent.Runner
	listener  net.Listener
	streams   map[string][]net.Conn // taskID → connected stream clients
	exitInfos map[string]ExitInfo    // taskID → cached exit info (brief)
	mu        sync.Mutex
	done      chan struct{}
	ready     chan struct{} // closed when Serve has set listener (or failed)
}

// New creates a new Daemon.
func New(database *db.DB) *Daemon {
	d := &Daemon{
		db:        database,
		streams:   make(map[string][]net.Conn),
		exitInfos: make(map[string]ExitInfo),
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
	}

	// Create runner with onFinish callback that caches exit info and
	// notifies stream clients by closing their connections.
	d.runner = agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		log.Printf("session exited: task=%s stopped=%v err=%v lastOutput=%d bytes",
			taskID, stopped, err, len(lastOutput))

		var errStr string
		if err != nil {
			errStr = err.Error()
		}

		d.mu.Lock()
		d.exitInfos[taskID] = ExitInfo{
			Err:        errStr,
			Stopped:    stopped,
			LastOutput: lastOutput,
		}
		conns := d.streams[taskID]
		delete(d.streams, taskID)
		d.mu.Unlock()

		// Signal stream EOF to all connected clients by closing their connections.
		log.Printf("session exited: task=%s closing %d stream clients", taskID, len(conns))
		for _, conn := range conns {
			conn.Close()
		}
	})

	return d
}

// Runner returns the underlying runner for direct access (e.g., AddWriter).
func (d *Daemon) Runner() *agent.Runner {
	return d.runner
}

// Serve starts listening on the given socket path and accepts connections.
// Blocks until Shutdown is called or the listener is closed.
func (d *Daemon) Serve(sockPath string) error {
	// Kill any existing daemon process before taking over the socket.
	killExistingDaemon(DefaultPIDPath())

	// Remove stale socket file.
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		close(d.ready) // unblock Shutdown even on listen failure
		return fmt.Errorf("listen: %w", err)
	}
	d.mu.Lock()
	d.listener = ln
	d.mu.Unlock()
	close(d.ready)

	// Write PID file.
	pidPath := DefaultPIDPath()
	if err := writePIDFile(pidPath); err != nil {
		ln.Close()
		return fmt.Errorf("pid file: %w", err)
	}

	// Register RPC service.
	svc := &RPCService{daemon: d}
	server := rpc.NewServer()
	if err := server.RegisterName("Daemon", svc); err != nil {
		ln.Close()
		return fmt.Errorf("register rpc: %w", err)
	}

	// Trap signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			d.Shutdown()
		case <-d.done:
		}
	}()

	log.Printf("daemon listening on %s (pid %d)", sockPath, os.Getpid())

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.done:
				return nil // clean shutdown
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go d.handleConn(conn, server)
	}
}

// handleConn dispatches a connection based on its first byte:
// 'R' for JSON-RPC, 'S' for output streaming.
func (d *Daemon) handleConn(conn net.Conn, server *rpc.Server) {
	defer conn.Close()

	// Read dispatch byte.
	var prefix [1]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return
	}

	switch prefix[0] {
	case 'R':
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	case 'S':
		d.handleStream(conn)
	default:
		log.Printf("conn: unknown prefix byte 0x%02x", prefix[0])
	}
}

// registerStream registers a stream connection for a task.
func (d *Daemon) registerStream(taskID string, conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streams[taskID] = append(d.streams[taskID], conn)
}

// unregisterStream removes a stream connection for a task.
func (d *Daemon) unregisterStream(taskID string, conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	conns := d.streams[taskID]
	for i, c := range conns {
		if c == conn {
			d.streams[taskID] = append(conns[:i], conns[i+1:]...)
			return
		}
	}
}

// Shutdown gracefully stops the daemon.
func (d *Daemon) Shutdown() {
	select {
	case <-d.done:
		return // already shutting down
	default:
		close(d.done)
	}

	log.Println("daemon shutting down...")

	// Wait for Serve to have set the listener (or failed to start).
	<-d.ready

	d.mu.Lock()
	ln := d.listener
	d.mu.Unlock()
	if ln != nil {
		ln.Close()
	}

	d.runner.StopAll()

	// Only clean up socket and PID files if we still own them.
	// A newer daemon may have already replaced these files — removing them
	// would break the newer daemon's stream connections.
	removeIfOwnedByPID(DefaultSocketPath(), DefaultPIDPath(), os.Getpid())
}

// writePIDFile atomically writes the current process PID to a file.
func writePIDFile(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readPIDFile reads the PID from a PID file. Returns 0 if the file
// doesn't exist or can't be parsed.
func readPIDFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// killExistingDaemon reads the PID file and kills the existing daemon
// process if it's still alive. Waits briefly for it to exit.
func killExistingDaemon(pidPath string) {
	pid := readPIDFile(pidPath)
	if pid == 0 || pid == os.Getpid() {
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	// Check if process is alive (signal 0 doesn't kill, just checks).
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return // process already dead
	}

	log.Printf("killing existing daemon pid=%d", pid)
	_ = proc.Signal(syscall.SIGTERM)

	// Wait up to 2 seconds for it to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return // exited
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Force kill if still alive.
	log.Printf("force-killing daemon pid=%d", pid)
	_ = proc.Signal(syscall.SIGKILL)
}

// removeIfOwnedByPID removes the socket and PID files only if the PID file
// still contains our PID. Prevents a zombie daemon from deleting a newer
// daemon's socket.
func removeIfOwnedByPID(sockPath, pidPath string, ourPID int) {
	currentPID := readPIDFile(pidPath)
	if currentPID != ourPID {
		log.Printf("skipping file cleanup: PID file has %d, we are %d", currentPID, ourPID)
		return
	}
	os.Remove(sockPath)
	os.Remove(pidPath)
}
