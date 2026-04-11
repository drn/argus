package agent

import (
	"fmt"
	"io"
	"log/slog"
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
	// Reserve the slot under the lock to prevent TOCTOU races: two concurrent
	// Start() calls for the same task ID could both pass an exists-check and
	// overwrite each other, leaking PTY fds and sandbox cleanup closures.
	r.mu.Lock()
	if _, exists := r.sessions[task.ID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("session already exists for task %s", task.ID)
	}
	// Place a nil sentinel so concurrent callers see the reservation.
	r.sessions[task.ID] = nil
	r.mu.Unlock()

	// On failure, remove the reservation.
	cleanup := func() {
		r.mu.Lock()
		if r.sessions[task.ID] == nil {
			delete(r.sessions, task.ID)
		}
		r.mu.Unlock()
	}

	slog.Info("runner.Start", "task", task.ID, "session", task.SessionID, "resume", resume, "pty", fmt.Sprintf("%dx%d", cols, rows), "dir", task.Worktree)

	cmd, sandboxCleanup, err := BuildCmd(task, cfg, resume)
	if err != nil {
		slog.Error("runner.Start: BuildCmd failed", "task", task.ID, "err", err)
		cleanup()
		return nil, err
	}
	slog.Info("runner.Start", "cmd", cmd.Args, "dir", cmd.Dir)

	sess, err := StartSession(task.ID, cmd, rows, cols)
	if err != nil {
		slog.Error("runner.Start: StartSession failed", "task", task.ID, "err", err)
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		cleanup()
		return nil, err
	}

	slog.Info("runner.Start: OK", "task", task.ID, "pid", sess.PID())

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
		slog.Info("runner: process exited", "task", task.ID, "pid", sess.PID())
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

		slog.Info("runner: exit details", "task", task.ID, "err", exitErr, "stopped", wasStopped, "lastOutputBytes", len(lastOutput))

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
// Returns nil for reserved-but-not-yet-started slots (nil sentinel).
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
	slog.Info("runner.Stop", "task", taskID, "pid", sess.PID())
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

	slog.Info("runner.StopAll", "sessions", len(ids))
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
