package agent

import (
	"fmt"
	"io"
	"log"
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
func (r *Runner) Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, error) {
	r.mu.Lock()
	if _, exists := r.sessions[task.ID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("session already exists for task %s", task.ID)
	}
	r.mu.Unlock()

	log.Printf("runner.Start: task=%s session=%s resume=%v pty=%dx%d dir=%s",
		task.ID, task.SessionID, resume, cols, rows, task.Worktree)

	cmd, sandboxCleanup, err := BuildCmd(task, cfg, resume)
	if err != nil {
		log.Printf("runner.Start: BuildCmd FAILED task=%s err=%v", task.ID, err)
		return nil, err
	}
	log.Printf("runner.Start: cmd=%v dir=%s", cmd.Args, cmd.Dir)

	sess, err := StartSession(task.ID, cmd, rows, cols)
	if err != nil {
		log.Printf("runner.Start: StartSession FAILED task=%s err=%v", task.ID, err)
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		return nil, err
	}

	log.Printf("runner.Start: OK task=%s pid=%d", task.ID, sess.PID())

	r.mu.Lock()
	r.sessions[task.ID] = sess
	r.mu.Unlock()

	// Watch for process exit. The onFinish callback is fired while the
	// session is still in the map so consumers (e.g., daemon exit info
	// cache) are populated before the session becomes invisible to Get().
	// The callback runs OUTSIDE the lock to avoid deadlocking if it
	// re-enters the runner (e.g., HasSession).
	go func() {
		<-sess.Done()
		log.Printf("runner: process exited task=%s pid=%d", task.ID, sess.PID())
		// Clean up sandbox config temp file
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		// Capture last output before removing the session so callers
		// can display error messages after the session is gone.
		lastOutput := sess.RecentOutput()
		exitErr := sess.Err()

		r.mu.Lock()
		wasStopped := r.stopped[task.ID]
		delete(r.stopped, task.ID)
		r.mu.Unlock()

		log.Printf("runner: exit details task=%s err=%v stopped=%v lastOutput=%d bytes",
			task.ID, exitErr, wasStopped, len(lastOutput))

		// Fire callback while session is still in the map.
		if r.onFinish != nil {
			r.onFinish(task.ID, exitErr, wasStopped, lastOutput)
		}

		// Now remove the session so Get() returns nil.
		r.mu.Lock()
		delete(r.sessions, task.ID)
		r.mu.Unlock()
	}()

	return sess, nil
}

// Get returns the session for a task, or nil if not found.
func (r *Runner) Get(taskID string) SessionHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess := r.sessions[taskID]
	if sess == nil {
		return nil
	}
	return sess
}

// Attach connects stdin/stdout to a running session's PTY.
// Blocks until detach or process exit.
func (r *Runner) Attach(taskID string, stdin io.Reader, stdout io.Writer) error {
	sess := r.getSession(taskID)
	if sess == nil {
		return ErrSessionNotFound
	}
	return sess.Attach(stdin, stdout)
}

// Detach disconnects from a running session without stopping it.
func (r *Runner) Detach(taskID string) {
	if sess := r.getSession(taskID); sess != nil {
		sess.Detach()
	}
}

// getSession returns the concrete *Session for internal use (e.g., Attach/Detach).
func (r *Runner) getSession(taskID string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[taskID]
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
	log.Printf("runner.Stop: task=%s pid=%d", taskID, sess.PID())
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

	log.Printf("runner.StopAll: stopping %d sessions", len(ids))
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

// Sessions returns a snapshot of all active sessions keyed by task ID.
// Used by the daemon's ListSessions RPC to avoid per-session Get() calls.
func (r *Runner) Sessions() map[string]*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*Session, len(r.sessions))
	for id, sess := range r.sessions {
		out[id] = sess
	}
	return out
}

// RunningAndIdle returns the task IDs of all active sessions and of idle
// sessions in a single pass under one lock acquisition.
func (r *Runner) RunningAndIdle() (running, idle []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	running = make([]string, 0, len(r.sessions))
	for id, sess := range r.sessions {
		running = append(running, id)
		if sess.IsIdle() {
			idle = append(idle, id)
		}
	}
	return running, idle
}

// HasSession returns true if a session exists for the task.
func (r *Runner) HasSession(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[taskID]
	return ok
}
