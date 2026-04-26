package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/inject"
	injectcodex "github.com/drn/argus/internal/inject/codex"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/vault"
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
	StreamLost bool // true when stream disconnected but process exit not confirmed
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
	ready     chan struct{}  // closed when Serve has set listener (or failed)
	sockPath  string         // set by Serve, used by cleanup
	pidPath   string         // set by Serve, used by cleanup
	mcpPort   int            // actual MCP HTTP port in use (set after listen)
	mcpServer    *mcp.Server    // set when KB is enabled, shut down in cleanup
	kbIndexer    *kb.Indexer    // set when KB is enabled, stopped in cleanup
	vaultWatcher stopper        // set when auto_create_tasks is enabled
	apiServer    *api.Server    // set when API is enabled, shut down in cleanup

	// Boot identity — recorded once at New() so the TUI can detect when the
	// on-disk binary has been rebuilt since the daemon started.
	binaryPath  string
	binaryMtime time.Time
	bootedAt    time.Time
}

// stopper is an interface for anything with a Stop() method.
type stopper interface {
	Stop()
}

// New creates a new Daemon.
func New(database *db.DB) *Daemon {
	d := &Daemon{
		db:        database,
		streams:   make(map[string][]net.Conn),
		exitInfos: make(map[string]ExitInfo),
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		bootedAt:  time.Now(),
	}

	// Capture the binary path + mtime at startup. The on-disk binary may be
	// rebuilt while the daemon keeps running with the old in-memory image —
	// the TUI compares its current binary mtime against this snapshot.
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		d.binaryPath = exe
		if st, err := os.Stat(exe); err == nil {
			d.binaryMtime = st.ModTime()
		}
	}

	// Create runner with onFinish callback that caches exit info and
	// notifies stream clients by closing their connections.
	d.runner = agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		slog.Info("session exited", "task", taskID, "stopped", stopped, "err", err, "lastOutputBytes", len(lastOutput))

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
		slog.Info("session exited, closing stream clients", "task", taskID, "clients", len(conns))
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
	// Derive PID path from socket path so tests using temp dirs don't
	// touch ~/.argus/ and accidentally kill a real running daemon.
	pidPath := filepath.Join(filepath.Dir(sockPath), "daemon.pid")
	d.sockPath = sockPath
	d.pidPath = pidPath

	// Kill any existing daemon process before taking over the socket.
	killExistingDaemon(pidPath)

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
	if err := writePIDFile(pidPath); err != nil {
		ln.Close()
		return fmt.Errorf("pid file: %w", err)
	}

	// Start MCP HTTP server and KB indexer (only when KB is enabled in settings).
	cfg := d.db.Config()
	if cfg.KB.Enabled {
		mcpSrv := mcp.New(d.db, cfg.KB.HTTPPort, cfg.KB.MetisVaultPath)
		mcpSrv.SetTaskManager(
			func(name, prompt, project, todoPath string) (*model.Task, error) {
				return HeadlessCreateTask(d.db, d.runner, name, prompt, project, todoPath)
			},
			d.db,
			d.runner,
		)
		d.mcpServer = mcpSrv
		actualPort, err := mcpSrv.ListenAndServe()
		if err != nil {
			slog.Error("mcp server error", "err", err)
		} else {
			d.mu.Lock()
			d.mcpPort = actualPort
			d.mu.Unlock()
			slog.Info("mcp server listening", "port", actualPort)

			// Inject MCP config into Claude Code and Codex.
			go func() {
				if err := inject.InjectGlobal(actualPort); err != nil {
					slog.Error("inject claude", "err", err)
				} else {
					slog.Info("inject claude", "port", actualPort)
				}
				if err := injectcodex.InjectGlobal(actualPort); err != nil {
					slog.Error("inject codex", "err", err)
				} else {
					slog.Info("inject codex", "port", actualPort)
				}
				if err := inject.SetClaudeProjectMcpTrust(); err != nil {
					slog.Error("inject claude trust", "err", err)
				}
			}()
		}

		// Start the KB indexer for the Metis vault.
		if cfg.KB.MetisVaultPath != "" {
			idx := kb.NewIndexer(d.db, cfg.KB.MetisVaultPath)
			d.kbIndexer = idx
			go func() {
				if err := idx.Start(); err != nil {
					slog.Error("kb indexer start", "err", err)
				}
			}()
		}
	}

	// Start vault watcher for auto-task creation (when enabled).
	// AutoStartTodos uses polling; AutoCreateTasks uses fsnotify.
	// AutoStartTodos implies AutoCreateTasks behavior (polling subsumes fsnotify).
	if cfg.KB.AutoStartTodos || cfg.KB.AutoCreateTasks {
		vaultPath := cfg.KB.ArgusVaultPath
		if vaultPath == "" {
			vaultPath = config.DefaultArgusVaultPath()
		}
		vw := vault.NewWatcher(d.db, vaultPath, func(name, prompt, project, todoPath string) (*model.Task, error) {
			return HeadlessCreateTask(d.db, d.runner, name, prompt, project, todoPath)
		})
		d.vaultWatcher = vw
		if cfg.KB.AutoStartTodos {
			interval := time.Duration(cfg.KB.AutoStartInterval) * time.Second
			if interval <= 0 {
				interval = time.Duration(config.DefaultAutoStartInterval) * time.Second
				slog.Warn("auto_start_interval not set, using default", "interval", interval)
			}
			go func() {
				if err := vw.StartPolling(interval); err != nil {
					slog.Error("vault poller start", "err", err)
				}
			}()
		} else {
			go func() {
				if err := vw.Start(); err != nil {
					slog.Error("vault watcher start", "err", err)
				}
			}()
		}
	}

	// Start HTTP API server (when enabled in settings).
	if cfg.API.Enabled {
		tokenPath := filepath.Join(db.DataDir(), "api-token")
		token, err := api.LoadOrCreateToken(tokenPath)
		if err != nil {
			slog.Error("api token error", "err", err)
		} else {
			apiSrv := api.New(d.db, d.runner, token, func(name, prompt, project, todoPath string) (*model.Task, error) {
				return HeadlessCreateTask(d.db, d.runner, name, prompt, project, todoPath)
			})
			d.apiServer = apiSrv
			apiPort, err := apiSrv.ListenAndServe(cfg.API.HTTPPort)
			if err != nil {
				slog.Error("api server error", "err", err)
			} else {
				slog.Info("api server listening", "port", apiPort)
			}
		}
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
		// Restore default signal handling so a subsequent SIGTERM from
		// killExistingDaemon (new daemon starting) terminates the process
		// instead of being swallowed by the buffered sigCh channel.
		signal.Stop(sigCh)
	}()

	slog.Info("daemon listening", "sockPath", sockPath, "pid", os.Getpid())

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.done:
				// Run cleanup on the main goroutine so it completes before
				// the process exits. Shutdown() only signals — it does not
				// do cleanup, because it runs on a different goroutine
				// (signal handler or RPC handler) that gets killed when
				// main() returns.
				d.cleanup()
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
		slog.Warn("conn: unknown prefix byte", "byte", fmt.Sprintf("0x%02x", prefix[0]))
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

// Shutdown signals the daemon to stop. It closes the done channel and the
// listener, causing the Serve accept loop to exit. Actual cleanup (StopAll,
// file removal) happens in Serve's exit path on the main goroutine — this
// ensures cleanup completes before the process exits.
func (d *Daemon) Shutdown() {
	select {
	case <-d.done:
		return // already shutting down
	default:
		close(d.done)
	}

	// Wait for Serve to have set the listener (or failed to start).
	<-d.ready

	d.mu.Lock()
	ln := d.listener
	d.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
}

// cleanup runs on the main goroutine (Serve's exit path) to ensure it
// completes before the process exits. If Shutdown ran these on its goroutine
// (signal/RPC handler), main() could return from Serve() first, killing
// the cleanup goroutine and leaving zombie agent processes + stale files.
func (d *Daemon) cleanup() {
	slog.Info("daemon shutting down")
	d.runner.StopAll()

	// Stop the vault watcher if running.
	if d.vaultWatcher != nil {
		d.vaultWatcher.Stop()
	}

	// Stop the KB indexer if running.
	if d.kbIndexer != nil {
		d.kbIndexer.Stop()
	}

	// Shut down the API HTTP server if running.
	if d.apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.apiServer.Shutdown(ctx); err != nil {
			slog.Error("api server shutdown", "err", err)
		}
	}

	// Shut down the MCP HTTP server if running.
	if d.mcpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.mcpServer.Shutdown(ctx); err != nil {
			slog.Error("mcp server shutdown", "err", err)
		}
	}

	// Only clean up socket and PID files if we still own them.
	// A newer daemon may have already replaced these files — removing them
	// would break the newer daemon's stream connections.
	removeIfOwnedByPID(d.sockPath, d.pidPath, os.Getpid())
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

	slog.Info("killing existing daemon", "pid", pid)
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
	slog.Warn("force-killing daemon", "pid", pid)
	_ = proc.Signal(syscall.SIGKILL)
}

// removeIfOwnedByPID removes the socket and PID files only if the PID file
// still contains our PID. Prevents a zombie daemon from deleting a newer
// daemon's socket.
func removeIfOwnedByPID(sockPath, pidPath string, ourPID int) {
	currentPID := readPIDFile(pidPath)
	if currentPID != ourPID {
		slog.Warn("skipping file cleanup", "pidFileOwner", currentPID, "ourPID", ourPID)
		return
	}
	os.Remove(sockPath)
	os.Remove(pidPath)
}
