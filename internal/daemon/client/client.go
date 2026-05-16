package client

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// rpcTimeout is the maximum time to wait for any single RPC call to the daemon.
// Prevents the TUI from hanging indefinitely if the daemon crashes.
// 2s is generous for a local Unix socket; anything slower indicates real trouble.
const rpcTimeout = 2 * time.Second

// ErrRPCTimeout is returned when an RPC call exceeds rpcTimeout.
var ErrRPCTimeout = errors.New("daemon RPC call timed out")

// ErrTestBinary is returned by AutoStart when invoked from a Go test binary.
// AutoStart fork/execs os.Executable() with "daemon start" — under `go test`
// that re-runs the entire test package as an orphaned child process, which
// is both a fork bomb (each child re-hits the same test path) and trashes
// the user's real ~/.argus/argusd symlink. The backstop is intentionally at
// the AutoStart layer so any caller that accidentally reaches it from a test
// is refused, not just the one we know about.
var ErrTestBinary = errors.New("AutoStart refused: running under a Go test binary")

// isTestBinary mirrors agent.isTestBinary (unexported there). Go's test
// framework compiles binaries with a .test suffix or under a _test/ path.
// Keep in sync with internal/agent/cleanup.go and internal/api/selfupdate.go.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}

// Compile-time assertion.
var _ agent.SessionProvider = (*Client)(nil)

// Client connects to the daemon and implements agent.SessionProvider.
type Client struct {
	rpc      *rpc.Client
	sockPath string
	sessions map[string]*RemoteSession
	mu       sync.Mutex
	closed   chan struct{} // closed by Close(); stops connectStream retries

	// leakedCalls tracks goroutines from timed-out RPC calls that are still
	// blocked in rpc.Call. Logged for observability — drain goroutines
	// decrement the counter when the RPC eventually completes.
	leakedCalls int

	// onSessionExit is called when a session's stream EOF is detected.
	// Includes exit info (error, stopped flag, last output) from the daemon.
	onSessionExit func(taskID string, info daemon.ExitInfo)
}

// Connect dials the daemon socket and returns a Client.
func Connect(sockPath string) (*Client, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}

	// Send RPC prefix byte.
	if _, err := conn.Write([]byte("R")); err != nil {
		conn.Close()
		return nil, err
	}

	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: sockPath,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}

	// Eagerly verify daemon is alive before returning the client.
	c.Ping() //nolint:errcheck — best-effort; health check will retry

	return c, nil
}

// OnSessionExit registers a callback invoked when a session's stream reports EOF.
// The callback receives exit info (error string, stopped flag, last output) from the daemon.
func (c *Client) OnSessionExit(fn func(taskID string, info daemon.ExitInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onSessionExit = fn
}

// Close shuts down the client and all stream connections.
// Signals all connectStream goroutines to stop retrying via the closed channel.
func (c *Client) Close() error {
	// Signal all goroutines to stop before closing individual sessions.
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}

	c.mu.Lock()
	sessions := make(map[string]*RemoteSession, len(c.sessions))
	for k, v := range c.sessions {
		sessions[k] = v
	}
	c.mu.Unlock()

	for _, rs := range sessions {
		rs.close()
	}
	return c.rpc.Close()
}

// Start requests the daemon to start a session and opens a stream for its output.
func (c *Client) Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (agent.SessionHandle, error) {
	uxlog.Log("client.Start: task=%s session=%s resume=%v", task.ID, task.SessionID, resume)
	req := &daemon.StartReq{
		TaskID:    task.ID,
		SessionID: task.SessionID,
		Prompt:    task.Prompt,
		Project:   task.Project,
		Backend:   task.Backend,
		Worktree:  task.Worktree,
		Branch:    task.Branch,
		Rows:      rows,
		Cols:      cols,
		Resume:    resume,
	}

	var resp daemon.StartResp
	if err := c.call("Daemon.StartSession", req, &resp); err != nil {
		uxlog.Log("client.Start: RPC FAILED task=%s err=%v", task.ID, err)
		return nil, err
	}
	if resp.Error != "" {
		uxlog.Log("client.Start: daemon error task=%s err=%s", task.ID, resp.Error)
		return nil, fmt.Errorf("daemon: %s", resp.Error)
	}

	uxlog.Log("client.Start: success task=%s pid=%d", task.ID, resp.PID)
	rs := c.getOrCreateSession(task.ID)
	rs.mu.Lock()
	rs.pid = resp.PID
	rs.mu.Unlock()
	return rs, nil
}

// Get returns a SessionHandle for the task, or nil if not found.
func (c *Client) Get(taskID string) agent.SessionHandle {
	c.mu.Lock()
	rs, ok := c.sessions[taskID]
	c.mu.Unlock()
	if ok {
		return rs
	}

	// Check with daemon if session exists.
	var info daemon.SessionInfo
	if err := c.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return nil
	}
	if !info.Alive {
		return nil
	}

	rs = c.getOrCreateSession(taskID)
	rs.updateInfo(info)
	return rs
}

// Stop stops a session via RPC.
func (c *Client) Stop(taskID string) error {
	var resp daemon.StatusResp
	if err := c.call("Daemon.StopSession", &daemon.TaskIDReq{TaskID: taskID}, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// StopAll stops all sessions via RPC. Errors are silently ignored since
// StopAll is typically called during shutdown where there's no recovery path.
func (c *Client) StopAll() {
	var resp daemon.StatusResp
	_ = c.call("Daemon.StopAll", &daemon.Empty{}, &resp)
}

// Shutdown asks the daemon to shut down gracefully.
func (c *Client) Shutdown() error {
	var resp daemon.StatusResp
	err := c.call("Daemon.Shutdown", &daemon.Empty{}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// updateSelfTimeout caps `go install ./...` runs. Generous to cover cold
// module downloads on a fresh GOPATH; the daemon will return sooner on
// success or compile failure.
const updateSelfTimeout = 10 * time.Minute

// UpdateSelf runs `go install ./...` against the configured Argus source path.
// Returns the combined command output and any error. The daemon is not
// restarted by this call — callers chain restartDaemon() afterward. Uses a
// long timeout because real builds can take 30s+; the default 2s rpcTimeout
// would always trip even though the daemon-side install completes.
func (c *Client) UpdateSelf() (string, error) {
	var resp daemon.UpdateSelfResp
	if err := c.callWithTimeout("Daemon.UpdateSelf", &daemon.Empty{}, &resp, updateSelfTimeout); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return resp.Output, fmt.Errorf("%s", resp.Error)
	}
	return resp.Output, nil
}

// ClipboardGet fetches any agent-staged text for a task. Returns empty
// string and ok=false when nothing is staged or RPC fails.
func (c *Client) ClipboardGet(taskID string) (string, bool) {
	var resp daemon.ClipboardGetResp
	if err := c.call("Daemon.ClipboardGet", &daemon.ClipboardGetReq{TaskID: taskID}, &resp); err != nil {
		return "", false
	}
	if !resp.OK {
		return "", false
	}
	return resp.Text, true
}

// ClipboardClear removes any agent-staged text for a task.
func (c *Client) ClipboardClear(taskID string) error {
	var resp daemon.StatusResp
	if err := c.call("Daemon.ClipboardClear", &daemon.ClipboardClearReq{TaskID: taskID}, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// Running returns task IDs of running sessions.
func (c *Client) Running() []string {
	var resp daemon.ListResp
	if err := c.call("Daemon.ListSessions", &daemon.Empty{}, &resp); err != nil {
		return nil
	}
	ids := make([]string, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		if s.Alive {
			ids = append(ids, s.TaskID)
		}
	}
	return ids
}

// Idle returns task IDs of idle sessions.
func (c *Client) Idle() []string {
	var resp daemon.ListResp
	if err := c.call("Daemon.ListSessions", &daemon.Empty{}, &resp); err != nil {
		return nil
	}
	var ids []string
	for _, s := range resp.Sessions {
		if s.Idle {
			ids = append(ids, s.TaskID)
		}
	}
	return ids
}

// RunningAndIdle returns running and idle task IDs in a single RPC call.
func (c *Client) RunningAndIdle() (running, idle []string) {
	var resp daemon.ListResp
	if err := c.call("Daemon.ListSessions", &daemon.Empty{}, &resp); err != nil {
		return nil, nil
	}
	running = make([]string, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		if s.Alive {
			running = append(running, s.TaskID)
		}
		if s.Idle {
			idle = append(idle, s.TaskID)
		}
	}
	return running, idle
}

// HasSession returns true if a session exists for the task.
func (c *Client) HasSession(taskID string) bool {
	var info daemon.SessionInfo
	if err := c.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return false
	}
	return info.Alive || info.PID != 0
}

// HasPendingRestart proxies Runner.HasPendingRestart over RPC. The TUI's
// handleSessionExitUI consults this when an exit notification arrives so it
// can skip the InProgress→InReview transition while the daemon is mid
// kick-restart for this task.
func (c *Client) HasPendingRestart(taskID string) bool {
	var resp daemon.PendingRestartResp
	if err := c.call("Daemon.HasPendingRestart", &daemon.TaskIDReq{TaskID: taskID}, &resp); err != nil {
		return false
	}
	return resp.Pending
}

// WorkDir returns the working directory of a session.
func (c *Client) WorkDir(taskID string) string {
	var info daemon.SessionInfo
	if err := c.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return ""
	}
	return info.WorkDir
}

// call wraps c.rpc.Call with a timeout so the TUI never hangs indefinitely
// if the daemon crashes. On timeout, a background goroutine drains the
// channel when the RPC eventually completes, preventing goroutine leaks.
func (c *Client) call(method string, args, reply any) error {
	return c.callWithTimeout(method, args, reply, rpcTimeout)
}

// callWithTimeout is like call but lets the caller pick the deadline. Used
// for legitimately long-running RPCs (e.g. UpdateSelf, which shells out to
// `go install`).
func (c *Client) callWithTimeout(method string, args, reply any, timeout time.Duration) error {
	ch := make(chan error, 1)
	go func() { ch <- c.rpc.Call(method, args, reply) }()
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		// Drain the channel in the background so the RPC goroutine can
		// exit when it eventually completes (e.g., socket error on daemon crash).
		c.mu.Lock()
		c.leakedCalls++
		leaked := c.leakedCalls
		c.mu.Unlock()
		uxlog.Log("client.call: RPC TIMEOUT method=%s leaked=%d", method, leaked)
		go func() {
			<-ch // wait for RPC goroutine to finish
			c.mu.Lock()
			c.leakedCalls--
			c.mu.Unlock()
		}()
		return ErrRPCTimeout
	}
}

// getOrCreateSession returns an existing RemoteSession or creates a new one
// with a stream connection.
func (c *Client) getOrCreateSession(taskID string) *RemoteSession {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rs, ok := c.sessions[taskID]; ok {
		return rs
	}

	rs := newRemoteSession(taskID, c)
	c.sessions[taskID] = rs

	// Open a stream connection in the background.
	go rs.connectStream(c.sockPath)

	return rs
}

// AutoStart launches the daemon as a background process and waits for it to
// be ready. Returns a connected client or an error.
//
// The body is delegated to autoStartFork (in autostart_fork.go) so the
// test-binary backstop is the only branch exercised under `go test`.
// autoStartFork is excluded from the coverage gate because exercising it
// would re-create the exact fork bomb ErrTestBinary exists to prevent.
func AutoStart(sockPath string) (*Client, error) {
	if isTestBinary() {
		return nil, ErrTestBinary
	}
	return autoStartFork(sockPath)
}

// WaitForShutdown polls until the daemon socket is gone (up to timeout).
func WaitForShutdown(sockPath string, timeout time.Duration) {
	const pollInterval = 50 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(pollInterval)
	}
}

// Ping verifies the daemon is responsive. Returns nil on success.
func (c *Client) Ping() error {
	var resp daemon.PongResp
	return c.call("Daemon.Ping", &daemon.Empty{}, &resp)
}

// BootInfo returns the daemon's boot-time identity (binary path + mtime).
func (c *Client) BootInfo() (daemon.BootInfoResp, error) {
	var resp daemon.BootInfoResp
	if err := c.call("Daemon.BootInfo", &daemon.Empty{}, &resp); err != nil {
		return daemon.BootInfoResp{}, err
	}
	return resp, nil
}

// removeSessionStreamLost cleans up a session from the client's map and fires
// the callback with StreamLost=true. Used when stream retries are exhausted or
// the daemon is unreachable — the process may still be alive.
func (c *Client) removeSessionStreamLost(taskID string) {
	uxlog.Log("client.removeSessionStreamLost: task=%s", taskID)
	c.mu.Lock()
	delete(c.sessions, taskID)
	fn := c.onSessionExit
	c.mu.Unlock()
	if fn != nil {
		fn(taskID, daemon.ExitInfo{StreamLost: true})
	}
}

// removeSession cleans up a session from the client's map, queries exit info
// from the daemon, and fires the callback.
func (c *Client) removeSession(taskID string) {
	uxlog.Log("client.removeSession: task=%s", taskID)
	c.mu.Lock()
	delete(c.sessions, taskID)
	fn := c.onSessionExit
	c.mu.Unlock()

	if fn != nil {
		// Query exit info from daemon before firing callback.
		var info daemon.ExitInfo
		err := c.call("Daemon.GetExitInfo", &daemon.TaskIDReq{TaskID: taskID}, &info)
		uxlog.Log("client.removeSession: task=%s exitInfo err=%v rpcErr=%v stopped=%v lastOutput=%d bytes",
			taskID, info.Err, err, info.Stopped, len(info.LastOutput))
		fn(taskID, info)
	}
}
