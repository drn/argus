package client

import (
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"sync"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/model"
)

// Compile-time assertion.
var _ agent.SessionProvider = (*Client)(nil)

// Client connects to the daemon and implements agent.SessionProvider.
type Client struct {
	rpc      *rpc.Client
	sockPath string
	sessions map[string]*RemoteSession
	mu       sync.Mutex

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
	if err := c.rpc.Call("Daemon.StartSession", req, &resp); err != nil {
		return nil, err
	}

	rs := c.getOrCreateSession(task.ID)
	rs.pid = resp.PID
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
	if err := c.rpc.Call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return nil
	}
	if !info.Alive && info.PID == 0 {
		return nil
	}

	rs = c.getOrCreateSession(taskID)
	rs.updateInfo(info)
	return rs
}

// Stop stops a session via RPC.
func (c *Client) Stop(taskID string) error {
	var resp daemon.StatusResp
	if err := c.rpc.Call("Daemon.StopSession", &daemon.TaskIDReq{TaskID: taskID}, &resp); err != nil {
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
	_ = c.rpc.Call("Daemon.StopAll", &daemon.Empty{}, &resp)
}

// Running returns task IDs of running sessions.
func (c *Client) Running() []string {
	var resp daemon.ListResp
	if err := c.rpc.Call("Daemon.ListSessions", &daemon.Empty{}, &resp); err != nil {
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
	if err := c.rpc.Call("Daemon.ListSessions", &daemon.Empty{}, &resp); err != nil {
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

// HasSession returns true if a session exists for the task.
func (c *Client) HasSession(taskID string) bool {
	var info daemon.SessionInfo
	if err := c.rpc.Call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return false
	}
	return info.Alive || info.PID != 0
}

// WorkDir returns the working directory of a session.
func (c *Client) WorkDir(taskID string) string {
	var info daemon.SessionInfo
	if err := c.rpc.Call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: taskID}, &info); err != nil {
		return ""
	}
	return info.WorkDir
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

// removeSession cleans up a session from the client's map, queries exit info
// from the daemon, and fires the callback.
func (c *Client) removeSession(taskID string) {
	c.mu.Lock()
	delete(c.sessions, taskID)
	fn := c.onSessionExit
	c.mu.Unlock()

	if fn != nil {
		// Query exit info from daemon before firing callback.
		var info daemon.ExitInfo
		c.rpc.Call("Daemon.GetExitInfo", &daemon.TaskIDReq{TaskID: taskID}, &info)
		fn(taskID, info)
	}
}
