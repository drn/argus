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
	"sync"
	"syscall"

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
}

// New creates a new Daemon.
func New(database *db.DB) *Daemon {
	d := &Daemon{
		db:        database,
		streams:   make(map[string][]net.Conn),
		exitInfos: make(map[string]ExitInfo),
		done:      make(chan struct{}),
	}

	// Create runner with onFinish callback that caches exit info and
	// notifies stream clients by closing their connections.
	d.runner = agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
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
	// Remove stale socket file.
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = ln

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

	if d.listener != nil {
		d.listener.Close()
	}

	d.runner.StopAll()

	// Clean up socket and PID files.
	os.Remove(DefaultSocketPath())
	os.Remove(DefaultPIDPath())
}

// writePIDFile atomically writes the current process PID to a file.
func writePIDFile(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
