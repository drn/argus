package agent

import (
	"fmt"
	"io"
	"sync"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// Runner manages multiple agent sessions keyed by task ID.
type Runner struct {
	mu       sync.Mutex
	sessions map[string]*Session
	stopped  map[string]bool // tracks task IDs where Stop was explicitly called
	onFinish func(taskID string, err error, stopped bool, lastOutput []byte)
}

// NewRunner creates a Runner. The onFinish callback is called (in a goroutine)
// when any managed session's process exits. lastOutput contains the final ring
// buffer contents so callers can display error messages after the session is gone.
func NewRunner(onFinish func(taskID string, err error, stopped bool, lastOutput []byte)) *Runner {
	return &Runner{
		sessions: make(map[string]*Session),
		stopped:  make(map[string]bool),
		onFinish: onFinish,
	}
}

// Start launches a new agent session for the given task.
// rows and cols set the initial PTY size (falls back to 80x24 if zero).
// If resume is true, the agent reconnects to an existing conversation via --resume.
// Returns an error if a session already exists for this task.
func (r *Runner) Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (*Session, error) {
	r.mu.Lock()
	if _, exists := r.sessions[task.ID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("session already exists for task %s", task.ID)
	}
	r.mu.Unlock()

	cmd, err := BuildCmd(task, cfg, resume)
	if err != nil {
		return nil, err
	}

	sess, err := StartSession(task.ID, cmd, rows, cols)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.sessions[task.ID] = sess
	r.mu.Unlock()

	// Watch for process exit
	go func() {
		<-sess.Done()
		// Capture last output before removing the session so callers
		// can display error messages after the session is gone.
		lastOutput := sess.RecentOutput()
		r.mu.Lock()
		delete(r.sessions, task.ID)
		wasStopped := r.stopped[task.ID]
		delete(r.stopped, task.ID)
		r.mu.Unlock()
		if r.onFinish != nil {
			r.onFinish(task.ID, sess.Err(), wasStopped, lastOutput)
		}
	}()

	return sess, nil
}

// Get returns the session for a task, or nil if not found.
func (r *Runner) Get(taskID string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[taskID]
}

// Attach connects stdin/stdout to a running session's PTY.
// Blocks until detach or process exit.
func (r *Runner) Attach(taskID string, stdin io.Reader, stdout io.Writer) error {
	sess := r.Get(taskID)
	if sess == nil {
		return ErrSessionNotFound
	}
	return sess.Attach(stdin, stdout)
}

// Detach disconnects from a running session without stopping it.
func (r *Runner) Detach(taskID string) {
	if sess := r.Get(taskID); sess != nil {
		sess.Detach()
	}
}

// Stop sends SIGTERM to a running session.
func (r *Runner) Stop(taskID string) error {
	r.mu.Lock()
	sess := r.sessions[taskID]
	if sess == nil {
		r.mu.Unlock()
		return ErrSessionNotFound
	}
	r.stopped[taskID] = true
	r.mu.Unlock()
	return sess.Stop()
}

// StopAll terminates all running sessions.
func (r *Runner) StopAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	for _, id := range ids {
		r.Stop(id)
	}
}

// Running returns the task IDs of all active sessions.
func (r *Runner) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	return ids
}

// Idle returns the task IDs of sessions that are alive but waiting for input.
func (r *Runner) Idle() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, sess := range r.sessions {
		if sess.IsIdle() {
			ids = append(ids, id)
		}
	}
	return ids
}

// WorkDir returns the effective working directory for a task's session.
// Returns empty string if no session exists.
func (r *Runner) WorkDir(taskID string) string {
	if sess := r.Get(taskID); sess != nil {
		return sess.WorkDir()
	}
	return ""
}

// HasSession returns true if a session exists for the task.
func (r *Runner) HasSession(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[taskID]
	return ok
}
