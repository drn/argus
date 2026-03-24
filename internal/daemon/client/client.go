package client

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// rpcTimeout is the maximum time to wait for any single RPC call to the daemon.
// Prevents the TUI from hanging indefinitely if the daemon crashes.
// 2s is generous for a local Unix socket; anything slower indicates real trouble.
const rpcTimeout = 2 * time.Second

// ErrRPCTimeout is returned when an RPC call exceeds rpcTimeout.
var ErrRPCTimeout = errors.New("daemon RPC call timed out")

// Compile-time assertion.
var _ agent.SessionProvider = (*Client)(nil)

// Client connects to the daemon and implements agent.SessionProvider.
type Client struct {
	rpc      *rpc.Client
	sockPath string
	sessions map[string]*RemoteSession
	mu       sync.Mutex

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

	return &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: sockPath,
		sessions: make(map[string]*RemoteSession),
	}, nil
}

// OnSessionExit registers a callback invoked when a session's stream reports EOF.
// The callback receives exit info (error string, stopped flag, last output) from the daemon.
func (c *Client) OnSessionExit(fn func(taskID string, info daemon.ExitInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onSessionExit = fn
}

// Close shuts down the client and all stream connections.
func (c *Client) Close() error {
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
	ch := make(chan error, 1)
	go func() { ch <- c.rpc.Call(method, args, reply) }()
	select {
	case err := <-ch:
		return err
	case <-time.After(rpcTimeout):
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
func AutoStart(sockPath string) (*Client, error) {
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

	cmd := exec.Command(daemonExe, "daemon", "start")
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from parent process group so the daemon survives TUI exit.
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// Release the child process so it isn't reaped when we exit.
	cmd.Process.Release()

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
