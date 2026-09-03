package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
)

// DefaultSupervisorSocketPath returns the session-supervisor's Unix socket
// path. It is INDEPENDENT of the daemon's socket: the supervisor runs as its
// own long-lived singleton so the daemon can bounce (to iterate on
// hera/coordination) without interrupting agents (see the design doc §4).
func DefaultSupervisorSocketPath() string {
	return filepath.Join(db.DataDir(), "supervisor.sock")
}

// DefaultSupervisorPIDPath returns the session-supervisor's PID file path.
func DefaultSupervisorPIDPath() string {
	return filepath.Join(db.DataDir(), "supervisor.pid")
}

// Supervisor is the always-parent session host (P1 of the session-supervisor
// stack — see context/plans/session-supervisor.md). It mounts the SAME
// *sessionCore the daemon mounts and serves the IDENTICAL first-byte R/S
// protocol on its OWN socket, so a future supervisor-client (P2) can drive it
// with the existing internal/daemon/client code unchanged.
//
// What makes it different from the daemon, by design:
//   - Its onFinish is exit-caching-ONLY: it caches ExitInfo from Cmd.Wait()
//     and closes stream conns, but never touches a DB, never flips task status,
//     never recaptures session IDs, never emits events. The supervisor has no
//     DB; the daemon fetches the cached ExitInfo via GetExitInfo (P2) and does
//     the DB flip + #707 transition itself. Because the supervisor is the
//     always-parent, its Cmd.Wait() observes the REAL exit code, so #707 is
//     preserved structurally across the supervisor→daemon boundary.
//   - It carries its OWN singleton trio (supervisor.lock/.sock/.pid) via the
//     P0 path-parameterized helpers, fully independent of the daemon's trio.
//     killExistingDaemon(sp.pid) here targets ONLY the supervisor pid; it can
//     never signal the daemon, and vice-versa — no split-brain.
//   - It owns none of the daemon's coordination machinery (scheduler, MCP, API,
//     hera-adopt reconciliation, push). It does ONLY PTY supervision, keeping its
//     code surface tiny so it almost never needs a restart (a supervisor
//     restart is the one event that still interrupts agents).
//
// P1 is DARK: nothing connects to the supervisor yet. The daemon is untouched
// and still uses its in-process runner. P2 wires the daemon as a client.
type Supervisor struct {
	*sessionCore
	done     chan struct{}
	ready    chan struct{} // closed when Serve has set listener (or failed)
	listener net.Listener
	sockPath string   // set by Serve, used by cleanup
	pidPath  string   // set by Serve, used by cleanup
	lockFile *os.File // singleton flock held for the supervisor's lifetime

	// Boot identity — recorded once at NewSupervisor so Hello can report it.
	// binaryHash is the SHA-256 content hash of the supervisor's own resolved
	// executable (the skew signal the daemon relays); vcs is the display-only
	// commit SHA + dirty flag.
	binaryPath  string
	binaryMtime time.Time
	binaryHash  string
	vcs         buildid.VCS
	bootedAt    time.Time
}

// NewSupervisor builds a Supervisor wired to an exit-caching-only onFinish.
// cfgFn supplies the config used to resolve backends / build the agent command
// in StartSession; the *Supervisor itself holds NO database handle — the caller
// (cmd/argus session-supervisor start) injects cfgFn from the DB so the
// supervisor's config tracks the user's real backends without coupling the type
// to persistence. A nil cfgFn falls back to config.DefaultConfig so the binary
// is runnable and tests can stay DB-free.
func NewSupervisor(cfgFn func() config.Config) *Supervisor {
	s := &Supervisor{
		done:     make(chan struct{}),
		ready:    make(chan struct{}),
		bootedAt: time.Now(),
	}

	// Capture the binary path + mtime + content hash at startup (mirrors
	// Daemon.New) so Hello can report supervisor staleness to the daemon, which
	// relays it to the TUI (D1). The hash is the gating signal; VCS is display.
	if exe, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		s.binaryPath = exe
		if st, serr := os.Stat(exe); serr == nil {
			s.binaryMtime = st.ModTime()
		}
		if h, herr := BinaryHashFile(exe); herr == nil {
			s.binaryHash = h
		}
	}
	s.vcs = buildid.Current()

	if cfgFn == nil {
		cfgFn = config.DefaultConfig
	}

	// Exit-caching-only onFinish. Mirrors the SHAPE of the daemon's exit
	// handling (ExitInfo build + stream-close) so the ExitInfo crossing the
	// supervisor→daemon boundary is byte-for-byte what crosses daemon→TUI today
	// — but WITHOUT the DB flip / session-ID recapture / clipboard / events,
	// which are the daemon's job (it fetches this ExitInfo via GetExitInfo).
	//
	// `core` is captured by reference and assigned just below; the closure only
	// fires after a session exits (post-Serve), by which time core is live —
	// identical to how Daemon.New captures the not-yet-assigned d.sessionCore.
	var core *sessionCore
	runner := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		slog.Info("supervisor session exited", "task", taskID, "stopped", stopped, "err", err, "lastOutputBytes", len(lastOutput))

		var errStr string
		if err != nil {
			errStr = err.Error()
		}
		// Stamp HasPendingRestart so the daemon can read it off the cached
		// ExitInfo without a separate RPC, exactly as the daemon stamps it today.
		pending := core.runner.HasPendingRestart(taskID)

		ei := ExitInfo{
			Err:            errStr,
			Stopped:        stopped,
			LastOutput:     lastOutput,
			PendingRestart: pending,
		}
		core.mu.Lock()
		core.exitInfos[taskID] = ei
		conns := core.streams[taskID]
		delete(core.streams, taskID)
		core.mu.Unlock()

		// Signal stream EOF to all connected clients by closing their conns.
		slog.Info("supervisor session exited, closing stream clients", "task", taskID, "clients", len(conns))
		for _, conn := range conns {
			conn.Close() //nolint:errcheck // best-effort EOF signal; conn is being discarded
		}
	})

	core = newSessionCore(runner, cfgFn, s.done)
	s.sessionCore = core
	return s
}

// supervisorRPC is the JSON-RPC surface the supervisor exposes. It embeds the
// SAME *sessionCore as the daemon's RPCService, so the ten session-scoped
// methods (Ping, StartSession, StopSession, StopAll, SessionStatus,
// ListSessions, HasPendingRestart, WriteInput, Resize, GetExitInfo) are
// promoted and register under "Daemon" exactly as the daemon registers them —
// the whole point: a future supervisor-client speaks the identical protocol.
// It adds only the supervisor-specific Hello handshake and Shutdown.
type supervisorRPC struct {
	*sessionCore
	sup *Supervisor
}

// Hello returns the supervisor's protocol version and boot identity. P2's
// daemon calls this first to feature-detect before using any newer RPC/field.
func (s *supervisorRPC) Hello(_ *Empty, resp *HelloResp) error {
	resp.ProtocolVersion = ProtocolVersion
	resp.BinaryPath = s.sup.binaryPath
	resp.BinaryMtime = s.sup.binaryMtime
	resp.BinaryHash = s.sup.binaryHash
	resp.VCS = s.sup.vcs
	resp.BootedAt = s.sup.bootedAt
	// The executed-surface version (v6) — the staleness signal the TUI actually
	// judges on. Compiled in, not measured: this supervisor IS the build whose
	// constants it reports.
	cur := CurrentSupervisorSurface()
	resp.SpawnSurface = cur.Spawn
	resp.StreamSurface = cur.Stream
	return nil
}

// Shutdown initiates a graceful supervisor shutdown. Mirrors Daemon.Shutdown's
// RPC: it signals on a separate goroutine so the RPC reply can flush first.
func (s *supervisorRPC) Shutdown(_ *Empty, resp *StatusResp) error {
	slog.Info("supervisor rpc.Shutdown requested")
	resp.OK = true
	go s.sup.Shutdown()
	return nil
}

// Serve acquires the supervisor's independent singleton, binds the socket, and
// runs the accept loop until shutdown. It is a stripped mirror of Daemon.Serve:
// flock + listener + RPC registration + signal trap + accept, with NONE of the
// daemon's DB sweeps (reconcile/hera/bounce) or coordination services. Returns
// ErrDaemonAlreadyRunning when another supervisor already holds the lock.
func (s *Supervisor) Serve(sockPath string) error {
	// Derive the singleton trio from the socket path (P0 path-parameterized
	// helper). For ".../supervisor.sock" this yields ".../supervisor.pid" and
	// ".../supervisor.lock" — fully independent of the daemon's daemon.* trio,
	// so a supervisor takeover never touches the daemon's flock/pid.
	sp := singletonPathsForSock(sockPath)
	s.sockPath = sockPath
	s.pidPath = sp.pid

	// Kill any existing SUPERVISOR process before taking over the socket.
	// killExistingDaemon is pid-file based; passing sp.pid scopes it to the
	// supervisor — it can never signal the daemon.
	killExistingDaemon(sp.pid)

	// Singleton guard: exactly one supervisor may own the socket. The flock
	// makes the loser of any startup race exit cleanly (same anti-split-brain
	// rationale as the daemon's lock — see gotchas/daemon-rpc.md).
	lockFile, lerr := acquireSingletonLock(sp.lock, daemonLockTimeout)
	if lerr != nil {
		close(s.ready) // unblock Shutdown waiters even on early return
		if errors.Is(lerr, ErrDaemonAlreadyRunning) {
			slog.Info("another supervisor already holds the singleton lock; exiting", "sock", sockPath)
			return ErrDaemonAlreadyRunning
		}
		return fmt.Errorf("acquire supervisor lock: %w", lerr)
	}
	s.lockFile = lockFile

	// Remove stale socket file.
	os.Remove(sockPath) //nolint:errcheck // best-effort; listen below surfaces a real bind failure

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		close(s.ready) // unblock Shutdown even on listen failure
		return fmt.Errorf("listen: %w", err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	close(s.ready)
	if err := writePIDFile(sp.pid); err != nil {
		ln.Close() //nolint:errcheck // returning a fatal error anyway
		return fmt.Errorf("pid file: %w", err)
	}

	// Register the RPC service under "Daemon" so the existing client speaks to
	// the supervisor unchanged. The promoted *sessionCore methods + Hello +
	// Shutdown are the supervisor's full surface.
	svc := &supervisorRPC{sessionCore: s.sessionCore, sup: s}
	server := rpc.NewServer()
	if err := server.RegisterName("Daemon", svc); err != nil {
		ln.Close() //nolint:errcheck // returning a fatal error anyway
		return fmt.Errorf("register rpc: %w", err)
	}

	// Trap signals for graceful shutdown (mirrors the daemon).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			s.Shutdown()
		case <-s.done:
		}
		// Restore default handling so a subsequent SIGTERM from a new
		// supervisor's killExistingDaemon terminates us instead of being
		// swallowed by the buffered channel.
		signal.Stop(sigCh)
	}()

	slog.Info("supervisor listening", "sockPath", sockPath, "pid", os.Getpid(), "protocol", ProtocolVersion)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				// Cleanup on the main goroutine so it completes before the
				// process exits (same rationale as Daemon.Serve).
				s.cleanup()
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go s.handleConn(conn, server)
	}
}

// handleConn dispatches a connection by its first byte: 'R' for JSON-RPC, 'S'
// for output streaming. Identical dispatch to Daemon.handleConn; the stream
// path resolves to the promoted *sessionCore.handleStream.
func (s *Supervisor) handleConn(conn net.Conn, server *rpc.Server) {
	defer conn.Close() //nolint:errcheck // best-effort close on a finished conn

	var prefix [1]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return
	}

	switch prefix[0] {
	case 'R':
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	case 'S':
		s.handleStream(conn)
	default:
		slog.Warn("supervisor conn: unknown prefix byte", "byte", fmt.Sprintf("0x%02x", prefix[0]))
	}
}

// Shutdown signals the supervisor to stop: it closes done and the listener,
// causing Serve's accept loop to exit and run cleanup. Mirrors Daemon.Shutdown.
func (s *Supervisor) Shutdown() {
	select {
	case <-s.done:
		return // already shutting down
	default:
		close(s.done)
	}

	<-s.ready // wait for Serve to have set the listener (or failed to start)

	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln != nil {
		ln.Close() //nolint:errcheck // unblocks Accept; close error is non-actionable
	}
}

// cleanup runs on Serve's exit goroutine. It stops every agent (StopAll),
// removes the socket/pid files iff we still own them, and releases the flock
// last so a supervisor waiting to take over only proceeds once cleanup is done.
// Mirrors Daemon.cleanup minus the daemon-only coordination teardown and the
// bounce live-tasks persistence (a bounce file is a DB/daemon concern).
func (s *Supervisor) cleanup() {
	slog.Info("supervisor shutting down")

	s.runner.StopAll()

	removeIfOwnedByPID(s.sockPath, s.pidPath, os.Getpid())

	if s.lockFile != nil {
		s.lockFile.Close() //nolint:errcheck // releasing the lock; close errors are non-actionable
		s.lockFile = nil
	}
}
