package agent

import (
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// pendingRestart holds the state needed to restart a session at new PTY
// dimensions after KickRerender stops it. Stored under Runner.mu. The entry
// stays in the map throughout the restart (so HasPendingRestart / Running
// surface "still alive" during the BuildCmd gap), but `consumed` flips true
// once an exit goroutine takes ownership — that prevents a fast-crashing
// resumed session's exit goroutine from re-entering the restart and looping.
type pendingRestart struct {
	task     *model.Task
	cfg      config.Config
	rows     uint16
	cols     uint16
	consumed bool
}

// Runner manages multiple agent sessions keyed by task ID.
type Runner struct {
	mu             sync.Mutex
	sessions       map[string]*Session
	stopped        map[string]bool // tracks task IDs where Stop was explicitly called
	pendingRestart map[string]*pendingRestart
	onFinish       func(taskID string, err error, stopped bool, lastOutput []byte)
}

// NewRunner creates a Runner. The onFinish callback is called (in a goroutine)
// when any managed session's process exits. lastOutput contains the final ring
// buffer contents so callers can display error messages after the session is gone.
func NewRunner(onFinish func(taskID string, err error, stopped bool, lastOutput []byte)) *Runner {
	return &Runner{
		sessions:       make(map[string]*Session),
		stopped:        make(map[string]bool),
		pendingRestart: make(map[string]*pendingRestart),
		onFinish:       onFinish,
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
		// sess.Done() blocks on readLoop draining, so the ring buffer
		// is guaranteed to hold every byte the PTY produced.
		lastOutput := sess.RecentOutput()
		exitErr := sess.Err()

		r.mu.Lock()
		wasStopped := r.stopped[task.ID]
		delete(r.stopped, task.ID)
		r.mu.Unlock()

		slog.Info("runner: exit details", "task", task.ID, "err", exitErr, "stopped", wasStopped, "lastOutputBytes", len(lastOutput))

		// Fire callback while session is still in the map. The daemon's
		// onFinish checks HasPendingRestart and skips the DB transition to
		// InReview when a kick-restart is in flight, so the row stays
		// InProgress through the restart.
		if r.onFinish != nil {
			r.onFinish(task.ID, exitErr, wasStopped, lastOutput)
		}

		// Take ownership of the pendingRestart entry. The entry STAYS in the
		// map throughout r.Start so consumers (handleStream wait,
		// HasPendingRestart, ListSessions, SessionStatus, RunningAndIdle) see
		// the gap as "still alive" and don't tear down. The `consumed` flag
		// prevents a fast-crashing resumed session's exit goroutine from
		// reading the same entry and looping another restart — only the
		// goroutine that flipped consumed=true runs the Start.
		r.mu.Lock()
		pending := r.pendingRestart[task.ID]
		shouldRestart := pending != nil && !pending.consumed
		if shouldRestart {
			pending.consumed = true
		}
		delete(r.sessions, task.ID)
		r.mu.Unlock()

		if shouldRestart {
			slog.Info("runner: restarting after kick", "task", task.ID, "cols", pending.cols, "rows", pending.rows)
			_, rerr := r.Start(pending.task, pending.cfg, pending.rows, pending.cols, true)
			// Clear the pending entry now that Start has returned; whether
			// it succeeded (new session in the map) or failed, the gap is
			// over and consumers should fall back to direct session lookup.
			r.mu.Lock()
			delete(r.pendingRestart, task.ID)
			r.mu.Unlock()
			if rerr != nil {
				// Restart failed. The daemon's onFinish skipped the DB
				// transition expecting a successful resume; without recovery
				// the row stays InProgress with no live session. Re-fire
				// onFinish — pendingRestart is now cleared so the daemon
				// runs the normal exit path (DB transition to InReview).
				slog.Error("runner: restart after kick failed, falling back to exit transition", "task", task.ID, "err", rerr)
				if r.onFinish != nil {
					r.onFinish(task.ID, rerr, true, lastOutput)
				}
			}
		}
	}()

	return sess, nil
}

// StartOrReattach returns the live session for task.ID if one already exists,
// otherwise behaves like Start. The reattached bool reports whether the
// returned handle is an existing live session (true) or a newly spawned one
// (false).
//
// Callers should treat reattached=true as a signal that the persisted task
// status drifted out of sync with the runner (e.g. a row was flipped to
// Pending while the daemon kept the PTY alive) and re-sync any state they
// own. Without this, calling Start directly on a desynced task returns
// "session already exists for task X".
func (r *Runner) StartOrReattach(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, bool, error) {
	if sess := r.Get(task.ID); sess != nil {
		return sess, true, nil
	}
	h, err := r.Start(task, cfg, rows, cols, resume)
	return h, false, err
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

// KickRerender stops a live session and queues a restart at the supplied
// dimensions. The restart fires from the exit goroutine in Start once the
// stopped session's process actually exits, with resume=true so the agent
// re-emits the entire conversation at the new PTY width via --session-id.
//
// The caller is responsible for evaluating the predicate (idle, has session
// ID, width delta exceeds margin) — KickRerender itself only enforces:
//   - a live session exists for the task
//   - no kick is already pending for this task
//
// Returns ErrSessionNotFound if no live session, or an error if a kick is
// already pending. Stop failures propagate as-is.
func (r *Runner) KickRerender(task *model.Task, cfg config.Config, rows, cols uint16) error {
	if task == nil {
		return fmt.Errorf("nil task")
	}
	r.mu.Lock()
	sess := r.sessions[task.ID]
	if sess == nil {
		r.mu.Unlock()
		return ErrSessionNotFound
	}
	if _, exists := r.pendingRestart[task.ID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("kick already pending for task %s", task.ID)
	}
	r.pendingRestart[task.ID] = &pendingRestart{
		task: task,
		cfg:  cfg,
		rows: rows,
		cols: cols,
	}
	r.stopped[task.ID] = true
	r.mu.Unlock()

	slog.Info("runner.KickRerender", "task", task.ID, "cols", cols, "rows", rows)
	if err := sess.Stop(); err != nil {
		// Stop failed — back out the pending entry so a future kick can run.
		r.mu.Lock()
		delete(r.pendingRestart, task.ID)
		delete(r.stopped, task.ID)
		r.mu.Unlock()
		return err
	}
	return nil
}

// HasPendingRestart reports whether a kick-restart is queued for the task.
// Callers (notably the daemon's onFinish) use this to decide whether to skip
// terminal-status transitions while a session is being restarted in place.
func (r *Runner) HasPendingRestart(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pendingRestart[taskID]
	return ok
}

// SetPendingRestartForTest injects a pendingRestart entry without going
// through the full Stop+exit-goroutine dance. Tests use this to exercise code
// paths that consult HasPendingRestart / PendingRestartIDs without depending
// on real PTY timing.
func (r *Runner) SetPendingRestartForTest(taskID string, pending bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pending {
		r.pendingRestart[taskID] = &pendingRestart{}
	} else {
		delete(r.pendingRestart, taskID)
	}
}

// PendingRestartIDs returns the task IDs that have a queued kick-restart but
// no current session in the runner. Used by the daemon's ListSessions RPC to
// surface synthetic "alive" entries during the kick gap so daemon-client
// reconcilers don't false-Complete the task.
func (r *Runner) PendingRestartIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.pendingRestart))
	for id := range r.pendingRestart {
		if _, hasSess := r.sessions[id]; !hasSess {
			out = append(out, id)
		}
	}
	return out
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

// Running returns the task IDs of all active sessions, including tasks whose
// session is between exit and a queued kick-restart (HasPendingRestart). Skips
// nil-sentinel entries (reserved-but-not-yet-started slots). The
// pending-restart inclusion keeps the API's idle reporting consistent with
// SessionStatus.Alive during the kick gap — without it, the SPA would treat
// the task as idle and skip reattaching to the new session.
func (r *Runner) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.sessions)+len(r.pendingRestart))
	for id, sess := range r.sessions {
		if sess != nil {
			ids = append(ids, id)
		}
	}
	for id := range r.pendingRestart {
		if _, alreadyListed := r.sessions[id]; !alreadyListed {
			ids = append(ids, id)
		}
	}
	return ids
}

// Idle returns the task IDs of sessions that are alive but waiting for input.
// Pending-restart tasks are NOT idle — they are mid-restart and the new agent
// is about to start emitting output. Skips nil-sentinel entries.
func (r *Runner) Idle() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, sess := range r.sessions {
		if sess != nil && sess.IsIdle() && r.pendingRestart[id] == nil {
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
// Skips nil-sentinel entries (reserved-but-not-yet-started slots).
func (r *Runner) Sessions() map[string]*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*Session, len(r.sessions))
	for id, sess := range r.sessions {
		if sess != nil {
			out[id] = sess
		}
	}
	return out
}

// RunningAndIdle returns the task IDs of all active sessions and of idle
// sessions in a single pass under one lock acquisition. Tasks with a queued
// kick-restart (HasPendingRestart) are reported as running but never idle —
// keeps the SPA's reattach gate (`status==='in_progress' && !task.idle`) live
// across the kick gap so it picks up the resumed session.
func (r *Runner) RunningAndIdle() (running, idle []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	running = make([]string, 0, len(r.sessions)+len(r.pendingRestart))
	for id, sess := range r.sessions {
		if sess == nil {
			continue
		}
		running = append(running, id)
		if sess.IsIdle() && r.pendingRestart[id] == nil {
			idle = append(idle, id)
		}
	}
	for id := range r.pendingRestart {
		if _, alreadyListed := r.sessions[id]; !alreadyListed {
			running = append(running, id)
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
