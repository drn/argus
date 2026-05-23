package apiclient

import (
	"context"
	"fmt"
	"sync"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// Compile-time assertion: Provider implements agent.SessionProvider.
var _ agent.SessionProvider = (*Provider)(nil)

// Provider implements agent.SessionProvider over HTTP using the apiclient
// Client. It mirrors the daemon-client provider (internal/daemon/client) but
// transports calls as REST requests instead of Unix-socket RPC. Each task
// gets a *Session whose local ring buffer is fed by an SSE stream from the
// /api/tasks/{id}/stream endpoint.
//
// Lifecycle: Start() creates or returns an existing Session, spawning the
// SSE reader goroutine on first attach. Sessions are tracked by task ID in
// the sessions map; the reader auto-removes itself on stream EOF and fires
// onSessionExit (if registered) so the TUI can flip status the same way it
// would for a daemon-client exit.
type Provider struct {
	c *Client

	mu       sync.Mutex
	sessions map[string]*Session
	closed   chan struct{} // closed by Close(); stops in-flight SSE readers

	// onSessionExit fires when a session's SSE stream EOFs or the server
	// reports the task is no longer alive. Wired by TUI startup, mirrors
	// daemon-client OnSessionExit.
	onSessionExit func(taskID string, info SessionExitInfo)
}

// SessionExitInfo carries the reason a session ended back to the TUI. The
// shape mirrors daemon.ExitInfo so the TUI's existing HandleSessionExit /
// NotifySessionExit handlers can consume both transports without specialised
// branches.
type SessionExitInfo struct {
	Err        string
	Stopped    bool
	StreamLost bool
	LastOutput []byte
}

// NewProvider returns a Provider wrapping the given Client.
func NewProvider(c *Client) *Provider {
	return &Provider{
		c:        c,
		sessions: make(map[string]*Session),
		closed:   make(chan struct{}),
	}
}

// OnSessionExit registers the callback. Safe to call before any sessions
// exist — Provider stores the function and dispatches once exit fires.
func (p *Provider) OnSessionExit(fn func(taskID string, info SessionExitInfo)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onSessionExit = fn
}

// Close cancels every in-flight SSE reader and clears the session map. The
// underlying http.Client is shared with the apiclient.Client and is not
// closed here — its idle connections cycle out naturally.
func (p *Provider) Close() {
	p.mu.Lock()
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	sessions := make([]*Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.sessions = make(map[string]*Session)
	p.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}

// Start creates the task on the server (or resumes it via POST /resume) and
// returns a Session bound to the resulting agent process. cfg is unused
// because the daemon owns the backend config — kept in the signature so the
// agent.SessionProvider interface holds.
//
// resume==true → POST /api/tasks/{id}/resume
// resume==false → start fresh; the server already created the task during
// /api/tasks POST, so this path is reached only when the caller is reviving
// a row whose session has exited.
func (p *Provider) Start(task *model.Task, _ config.Config, rows, cols uint16, resume bool) (agent.SessionHandle, error) {
	ctx := context.Background()
	// The TUI's CreateAndStart path goes through POST /api/tasks for fresh
	// tasks, which spawns the session inside the server. By the time Start
	// is called here, the row is already in_progress and a server-side
	// session exists; ResumeTask re-attaches (or restarts) regardless of
	// whether the caller asked for resume semantics — the `resume`
	// parameter is meaningful to the local in-process Runner but a no-op
	// at this HTTP boundary.
	_ = resume
	if _, err := p.c.ResumeTask(ctx, task.ID); err != nil {
		return nil, fmt.Errorf("apiclient.Start: resume task=%s: %w", task.ID, err)
	}

	s := p.getOrCreateSession(task.ID)
	// Best-effort initial size sync.
	_, _ = p.c.Resize(ctx, task.ID, rows, cols)
	return s, nil
}

// Stop ends the agent session for the task via REST.
func (p *Provider) Stop(taskID string) error {
	return p.c.StopTask(context.Background(), taskID)
}

// StopAll halts every running session. Master-only — Provider operates with
// the master token in local mode and a device token in remote mode; in the
// latter case the server returns 403.
func (p *Provider) StopAll() {
	_, _ = p.c.StopAll(context.Background())
}

// Get returns a SessionHandle for the task, or nil when no session is alive
// on the server. Caches the *Session locally and reuses it for repeat calls.
func (p *Provider) Get(taskID string) agent.SessionHandle {
	p.mu.Lock()
	s, ok := p.sessions[taskID]
	p.mu.Unlock()
	if ok {
		return s
	}
	// Check server.
	state, err := p.c.GetSessionState(context.Background())
	if err != nil {
		return nil
	}
	alive := false
	for _, id := range state.Running {
		if id == taskID {
			alive = true
			break
		}
	}
	if !alive {
		return nil
	}
	return p.getOrCreateSession(taskID)
}

// Running returns the IDs of every alive session as reported by the server.
func (p *Provider) Running() []string {
	state, err := p.c.GetSessionState(context.Background())
	if err != nil {
		return nil
	}
	return state.Running
}

// Idle returns the IDs of every idle session as reported by the server.
func (p *Provider) Idle() []string {
	state, err := p.c.GetSessionState(context.Background())
	if err != nil {
		return nil
	}
	return state.Idle
}

// RunningAndIdle returns both lists in a single HTTP request.
func (p *Provider) RunningAndIdle() (running, idle []string) {
	state, err := p.c.GetSessionState(context.Background())
	if err != nil {
		return nil, nil
	}
	return state.Running, state.Idle
}

// HasSession reports whether the server tracks an alive session for the task.
func (p *Provider) HasSession(taskID string) bool {
	state, err := p.c.GetSessionState(context.Background())
	if err != nil {
		return false
	}
	for _, id := range state.Running {
		if id == taskID {
			return true
		}
	}
	return false
}

// WorkDir returns the worktree path for a task. Fetched via GET /api/tasks/{id}.
func (p *Provider) WorkDir(taskID string) string {
	t, err := p.c.GetTask(context.Background(), taskID)
	if err != nil {
		return ""
	}
	return t.WorktreePath
}

// HasPendingRestart proxies to the server's /api/sessions/{id}/pending-restart.
func (p *Provider) HasPendingRestart(taskID string) bool {
	pending, err := p.c.HasPendingRestart(context.Background(), taskID)
	if err != nil {
		return false
	}
	return pending
}

// getOrCreateSession is the internal accessor used by Start/Get. Holds the
// provider lock for the lookup-or-create critical section so two concurrent
// callers don't both spawn a stream reader for the same task.
func (p *Provider) getOrCreateSession(taskID string) *Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[taskID]; ok {
		return s
	}
	s := newSession(taskID, p)
	p.sessions[taskID] = s
	go s.runStream()
	return s
}

// removeSession deletes a session from the map and fires the onSessionExit
// callback. Called by the stream reader on EOF or by Close.
func (p *Provider) removeSession(taskID string, info SessionExitInfo) {
	p.mu.Lock()
	delete(p.sessions, taskID)
	fn := p.onSessionExit
	p.mu.Unlock()
	if fn != nil {
		fn(taskID, info)
	}
}
