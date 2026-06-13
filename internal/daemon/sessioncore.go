package daemon

import (
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// streamPendingRestartWaitInterval is how often handleStream checks whether
// a kick-restart's new session has appeared while waiting at the gap.
const streamPendingRestartWaitInterval = 50 * time.Millisecond

// streamPendingRestartMaxWait is the upper bound on how long handleStream
// will block waiting for a kick-restart's new session to appear before giving
// up. Tuned to comfortably cover sandboxed Claude/Codex cold starts (~1-3s)
// without making truly-dead sessions linger.
const streamPendingRestartMaxWait = 10 * time.Second

// sessionCore is the reusable session-serving substrate: the agent.Runner, the
// per-task stream-conn registry, the brief exit-info cache, and the R/S stream
// handler + session-scoped JSON-RPC methods. It is the seam the daemon mounts
// today and the session-supervisor (P1) mounts tomorrow — see
// context/plans/session-supervisor.md. Both `*Daemon` and `*RPCService` embed
// a *sessionCore so every existing `d.runner`/`d.mu`/`d.streams`/`d.exitInfos`
// call site and every session RPC method (Daemon.StartSession, …) resolves via
// promotion, keeping behavior byte-identical.
//
// The mounting process injects the runner — so it owns the onFinish wiring (DB
// status flips, session-ID recapture, events for the daemon; just exit caching
// for the supervisor) — and the shutdown `done` channel. The core never touches
// the DB; it is pure PTY/session plumbing.
type sessionCore struct {
	// runner is the session backend, typed as the agent.SessionRunner interface
	// (not concrete *agent.Runner) so the SAME core serves two backends without
	// any handler knowing which: the daemon/supervisor mount an in-process
	// *agent.Runner (supervisor OFF, or the supervisor itself), while a daemon in
	// supervisor-ON mode mounts a supervisor-client *client.Client. Both satisfy
	// SessionRunner. Behavior under an in-process runner is byte-identical to
	// pre-P2 — interface dispatch resolves to the exact same methods.
	runner agent.SessionRunner
	cfgFn  func() config.Config
	done   <-chan struct{}

	mu        sync.Mutex
	streams   map[string][]net.Conn // taskID → connected stream clients
	exitInfos map[string]ExitInfo   // taskID → cached exit info (brief)
}

// rawSessionLister is the in-process runner's listing surface: it returns live
// *agent.Session handles. *agent.Runner satisfies it; a supervisor-client does
// not (it has no local sessions to iterate). ListSessions type-asserts to this
// for the OFF/supervisor path so that path stays byte-identical.
type rawSessionLister interface {
	Sessions() map[string]*agent.Session
	PendingRestartIDs() []string
}

// sessionInfoLister is the supervisor-client's listing surface: it relays the
// already-built SessionInfo (synthetic pending-restart entries included) from
// the supervisor, because the daemon cannot iterate the supervisor's in-process
// sessions. *client.Client satisfies it via ListSessionInfo.
type sessionInfoLister interface {
	ListSessionInfo() []SessionInfo
}

// newSessionCore wires a runner, a config accessor, and a shutdown channel into
// the session-serving substrate. The maps are owned here; the caller retains
// ownership of the runner (so it controls onFinish) and the done channel.
func newSessionCore(runner agent.SessionRunner, cfgFn func() config.Config, done <-chan struct{}) *sessionCore {
	return &sessionCore{
		runner:    runner,
		cfgFn:     cfgFn,
		done:      done,
		streams:   make(map[string][]net.Conn),
		exitInfos: make(map[string]ExitInfo),
	}
}

// Ping verifies the session server is responsive.
func (c *sessionCore) Ping(_ *Empty, resp *PongResp) error {
	resp.OK = true
	return nil
}

// StartSession starts a new agent session.
func (c *sessionCore) StartSession(req *StartReq, resp *StartResp) error {
	slog.Info("rpc.StartSession", "task", req.TaskID, "session", req.SessionID, "project", req.Project, "resume", req.Resume, "cols", req.Cols, "rows", req.Rows, "worktree", req.Worktree)

	task := &model.Task{
		ID:        req.TaskID,
		SessionID: req.SessionID,
		Prompt:    req.Prompt,
		Project:   req.Project,
		Backend:   req.Backend,
		Model:     req.Model,
		Worktree:  req.Worktree,
		Branch:    req.Branch,
	}

	cfg := c.cfgFn()
	sess, err := c.runner.Start(task, cfg, req.Rows, req.Cols, req.Resume)
	if err != nil {
		slog.Error("rpc.StartSession failed", "task", req.TaskID, "err", err)
		resp.Error = err.Error()
		return nil
	}
	resp.PID = sess.PID()
	slog.Info("rpc.StartSession ok", "task", req.TaskID, "pid", resp.PID)
	return nil
}

// StopSession stops a running session.
func (c *sessionCore) StopSession(req *TaskIDReq, resp *StatusResp) error {
	slog.Info("rpc.StopSession", "task", req.TaskID)
	if err := c.runner.Stop(req.TaskID); err != nil {
		slog.Error("rpc.StopSession failed", "task", req.TaskID, "err", err)
		resp.Error = err.Error()
		return nil
	}
	slog.Info("rpc.StopSession ok", "task", req.TaskID)
	resp.OK = true
	return nil
}

// StopAll stops all running sessions.
func (c *sessionCore) StopAll(_ *Empty, resp *StatusResp) error {
	slog.Info("rpc.StopAll")
	c.runner.StopAll()
	slog.Info("rpc.StopAll ok")
	resp.OK = true
	return nil
}

// SessionStatus returns info about a single session.
func (c *sessionCore) SessionStatus(req *TaskIDReq, resp *SessionInfo) error {
	sess := c.runner.Get(req.TaskID)
	if sess == nil {
		resp.TaskID = req.TaskID
		// During the brief gap between a kick-restart's old-session exit and
		// the new session's slot being filled, report Alive=true so stream
		// clients retry the connection instead of giving up and tearing down
		// their local UI state. The actual liveness will be reported once
		// the new session is in place.
		if c.runner.HasPendingRestart(req.TaskID) {
			resp.Alive = true
		}
		return nil
	}
	cols, rows := sess.PTYSize()
	initCols, initRows := sess.InitialPTYSize()
	resp.TaskID = req.TaskID
	resp.Alive = sess.Alive()
	resp.Idle = sess.IsIdle()
	resp.PID = sess.PID()
	resp.Cols = cols
	resp.Rows = rows
	resp.InitialCols = initCols
	resp.InitialRows = initRows
	resp.WorkDir = sess.WorkDir()
	resp.TotalWritten = sess.TotalWritten()
	return nil
}

// ListSessions returns info about all running sessions, plus synthetic
// Alive=true entries for tasks with a queued kick-restart but no current
// session (the brief gap between exit and Start). Without these synthetic
// entries, daemon-client reconcilers (TUI tick) see InProgress + not-running
// → mark Complete after recentStartGrace, racing the imminent restart.
func (c *sessionCore) ListSessions(_ *Empty, resp *ListResp) error {
	// Supervisor-client backend (daemon in ON mode): relay the supervisor's
	// already-built SessionInfo. The daemon holds no local *agent.Session set to
	// iterate, so the in-process path below cannot apply.
	if lister, ok := c.runner.(sessionInfoLister); ok {
		resp.Sessions = lister.ListSessionInfo()
		return nil
	}

	// In-process runner backend (OFF / the supervisor itself): build SessionInfo
	// from the live *agent.Session set + synthetic pending-restart entries.
	// Byte-identical to pre-P2.
	rl, ok := c.runner.(rawSessionLister)
	if !ok {
		return nil
	}
	sessions := rl.Sessions()
	pending := rl.PendingRestartIDs()
	resp.Sessions = make([]SessionInfo, 0, len(sessions)+len(pending))
	for id, sess := range sessions {
		cols, rows := sess.PTYSize()
		initCols, initRows := sess.InitialPTYSize()
		resp.Sessions = append(resp.Sessions, SessionInfo{
			TaskID:       id,
			Alive:        sess.Alive(),
			Idle:         sess.IsIdle(),
			PID:          sess.PID(),
			Cols:         cols,
			Rows:         rows,
			InitialCols:  initCols,
			InitialRows:  initRows,
			WorkDir:      sess.WorkDir(),
			TotalWritten: sess.TotalWritten(),
		})
	}
	// Synthetic entries for the kick-restart gap. Mirrors SessionStatus's
	// Alive=true synthetic when a single-task lookup hits the same window.
	for _, id := range pending {
		resp.Sessions = append(resp.Sessions, SessionInfo{
			TaskID: id,
			Alive:  true,
		})
	}
	return nil
}

// HasPendingRestart reports whether the runner has a kick-restart queued
// for this task. The TUI consults this from handleSessionExitUI so it knows
// to skip the InProgress→InReview transition while the daemon is mid-restart.
func (c *sessionCore) HasPendingRestart(req *TaskIDReq, resp *PendingRestartResp) error {
	resp.Pending = c.runner.HasPendingRestart(req.TaskID)
	return nil
}

// WriteInput sends data to a session's PTY stdin.
func (c *sessionCore) WriteInput(req *WriteReq, resp *StatusResp) error {
	sess := c.runner.Get(req.TaskID)
	if sess == nil {
		resp.Error = "session not found"
		return nil
	}
	if _, err := sess.WriteInput(req.Data); err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// Resize changes a session's PTY dimensions.
func (c *sessionCore) Resize(req *ResizeReq, resp *StatusResp) error {
	sess := c.runner.Get(req.TaskID)
	if sess == nil {
		resp.Error = "session not found"
		return nil
	}
	if err := sess.Resize(req.Rows, req.Cols); err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// KickRerender stops a session and queues an in-place restart at new dimensions
// (protocol v2). The supervisor serves this so the daemon's API resize path can
// drive a rerender through the supervisor's runner — the pendingRestart
// bookkeeping that keeps the intervening exit from flipping task status MUST run
// where the session lives. cfg is resolved here via cfgFn (not shipped on the
// wire). Promoted onto both *Daemon and *Supervisor; only the supervisor's is
// actually invoked in ON mode (the daemon's API client targets supervisor.sock),
// but keeping it uniform on the core means a future direct caller works too.
func (c *sessionCore) KickRerender(req *KickReq, resp *StatusResp) error {
	slog.Info("rpc.KickRerender", "task", req.TaskID, "cols", req.Cols, "rows", req.Rows)
	task := &model.Task{
		ID:        req.TaskID,
		SessionID: req.SessionID,
		Prompt:    req.Prompt,
		Project:   req.Project,
		Backend:   req.Backend,
		Model:     req.Model,
		Worktree:  req.Worktree,
		Branch:    req.Branch,
	}
	if err := c.runner.KickRerender(task, c.cfgFn(), req.Rows, req.Cols); err != nil {
		slog.Error("rpc.KickRerender failed", "task", req.TaskID, "err", err)
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// GetExitInfo returns cached exit info for a finished session.
// Returns empty ExitInfo if the session is still running or info has expired.
func (c *sessionCore) GetExitInfo(req *TaskIDReq, resp *ExitInfo) error {
	c.mu.Lock()
	info, ok := c.exitInfos[req.TaskID]
	if ok {
		delete(c.exitInfos, req.TaskID) // consume once
	}
	c.mu.Unlock()

	if ok {
		*resp = info
	}
	return nil
}

// registerStream registers a stream connection for a task.
func (c *sessionCore) registerStream(taskID string, conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streams[taskID] = append(c.streams[taskID], conn)
}

// unregisterStream removes a stream connection for a task.
func (c *sessionCore) unregisterStream(taskID string, conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.streams[taskID]
	for i, cc := range conns {
		if cc == conn {
			c.streams[taskID] = append(conns[:i], conns[i+1:]...)
			return
		}
	}
}

// handleStream processes a stream connection. The client sends a JSON
// StreamHeader, then the daemon registers the connection as a writer on
// the session. Output flows as raw bytes until the session exits or the
// client disconnects.
func (c *sessionCore) handleStream(conn net.Conn) {
	var header StreamHeader
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&header); err != nil {
		slog.Error("stream header decode error", "err", err)
		return
	}

	sess := c.runner.Get(header.TaskID)
	// If a kick-restart is in flight (between the old session's exit and the
	// new session's slot being filled), wait briefly for the new session to
	// land instead of rejecting the client immediately. Without this, TUI
	// clients reconnecting during a kick gap exhaust their retry budget
	// (3×500ms = 1.5s) and tear down the local handle. The wait is bounded
	// and short-circuits as soon as the new session appears or the kick is
	// no longer in flight.
	if sess == nil && c.runner.HasPendingRestart(header.TaskID) {
		deadline := time.Now().Add(streamPendingRestartMaxWait)
	wait:
		for time.Now().Before(deadline) {
			select {
			case <-c.done:
				return // daemon shutting down — abandon the wait
			case <-time.After(streamPendingRestartWaitInterval):
			}
			sess = c.runner.Get(header.TaskID)
			if sess != nil {
				slog.Info("stream: attached to resumed session after kick gap", "task", header.TaskID)
				break wait
			}
			if !c.runner.HasPendingRestart(header.TaskID) {
				break wait // restart abandoned (failed Start) — no new session coming
			}
		}
	}
	if sess == nil {
		slog.Warn("stream: session not found", "task", header.TaskID)
		return
	}

	slog.Info("stream connected", "task", header.TaskID, "since", header.Since)
	c.registerStream(header.TaskID, conn)
	defer c.unregisterStream(header.TaskID, conn)

	// AddWriterFromTolerant replays only [Since, currentTotal) before attaching
	// live — reconnects whose client ring already contains bytes ≤ Since don't
	// see the daemon ring replayed on top. Tolerant variant: conn.Write may
	// block on kernel socket flow control, so the replay runs outside the
	// session mutex (accepting a tiny gap rather than stalling readLoop).
	sess.AddWriterFromTolerant(conn, header.Since)
	defer sess.RemoveWriter(conn)

	// Block until the session exits or the client disconnects.
	// We detect client disconnect by trying to read from the connection.
	// The client doesn't send anything on the stream after the header,
	// so a read will block until the connection is closed.
	select {
	case <-sess.Done():
		slog.Info("stream: session exited", "task", header.TaskID)
	case <-c.done:
		slog.Info("stream: daemon shutting down", "task", header.TaskID)
	case <-waitForClose(conn):
		slog.Info("stream: client disconnected", "task", header.TaskID)
	}
}

// waitForClose returns a channel that closes when the connection is closed.
func waitForClose(conn net.Conn) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		conn.Read(buf) //nolint:errcheck // blocks until close; the error IS the close signal
		close(ch)
	}()
	return ch
}
