package daemon

import (
	"fmt"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
)

// HeraRecycleRunner implements hera.RecycleRunner against the daemon's real
// session runner and DB (add-coordinator-context-management D5). It is the
// seam recycle_test.go's fakeRecycleRunner stands in for in unit tests — this
// is the concrete, production version wired into the daemon's background
// recycle sweep and (Stage 7) the rail's human-forced keybinding via the
// exported NewHeraRecycleRunner (internal/tui/heraactions.go builds one
// directly since the TUI's hera mutation layer already operates on *db.DB +
// agent.SessionRunner in local mode, same pattern as its other hera ops).
type HeraRecycleRunner struct {
	database *db.DB
	runner   agent.SessionRunner
	cfgFn    func() config.Config
}

// NewHeraRecycleRunner builds the production hera.RecycleRunner. Exported so
// both the daemon's background RecycleWatcher and the TUI's human-forced `B`
// rail keybinding share the identical kill/restart implementation rather than
// each hand-rolling their own.
func NewHeraRecycleRunner(database *db.DB, runner agent.SessionRunner, cfgFn func() config.Config) *HeraRecycleRunner {
	return &HeraRecycleRunner{database: database, runner: runner, cfgFn: cfgFn}
}

var _ hera.RecycleRunner = (*HeraRecycleRunner)(nil)

// IsIdle reports whether taskID's live session is currently idle. A missing
// session (already exited, or never started) is treated as idle — nothing is
// actively producing output, so a self-service recycle should proceed rather
// than wait forever for a session that no longer exists.
func (r *HeraRecycleRunner) IsIdle(taskID string) bool {
	sess := r.runner.Get(taskID)
	return sess == nil || sess.IsIdle()
}

// StopStrayJobs cleans up any Claude Code background job tied to sessionID
// before the caller restarts taskID (design.md Risks: task_stop does not kill
// everything).
func (r *HeraRecycleRunner) StopStrayJobs(taskID, sessionID string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("stop stray jobs: load task %s: %w", taskID, err)
	}
	return agent.StopStrayJobs(task, r.cfgFn(), sessionID)
}

// Restart assembles the fresh session's seed prompt (mission + plan-DAG
// state + handoff note) for the already-resolved roleID, clears any stale
// SessionID so BuildCmd starts genuinely fresh rather than colliding on an
// already-used UUID, persists both, and hands off to the runner's same-task
// recycle primitive. roleID is resolved directly via HeraLiveBindingByRole —
// never re-derived from taskID alone — because a task holding 2+ live
// bindings (e.g. a worker in one orchestrator and a coordinator in another)
// would make a task-keyed lookup (HeraLiveBindingByTask) ambiguous.
func (r *HeraRecycleRunner) Restart(taskID string, roleID int64) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("recycle restart: load task %s: %w", taskID, err)
	}

	binding, err := r.database.HeraLiveBindingByRole(roleID)
	if err != nil {
		return fmt.Errorf("recycle restart: resolve binding for role %d: %w", roleID, err)
	}

	seedPrompt, err := hera.BuildRecycleSeedPrompt(r.database, binding.RoleID)
	if err != nil {
		return fmt.Errorf("recycle restart: build seed prompt for role %d: %w", binding.RoleID, err)
	}

	// Capture the outgoing session's PTY size before Recycle stops it, so the
	// fresh session opens at the same dimensions rather than falling back to
	// Start's 80x24 default.
	sess := r.runner.Get(taskID)
	var rows, cols uint16
	if sess != nil {
		c, rw := sess.PTYSize()
		cols, rows = uint16(c), uint16(rw) //nolint:gosec // bounded by terminal cell count
	}

	// task.SessionID MUST be cleared before Recycle: BuildCmd's non-resume
	// branch pins any non-empty SessionID via --session-id, and the OLD
	// (already-used) UUID would collide rather than mint a fresh session.
	task.SessionID = ""
	task.Prompt = seedPrompt
	if err := r.database.Update(task); err != nil {
		return fmt.Errorf("recycle restart: persist cleared session id for task %s: %w", taskID, err)
	}

	if sess == nil {
		// No live session to stop (already exited between the recycle request
		// and this restart). IsIdle already treats this as "proceed" — but
		// Runner.Recycle requires an existing session to stop and would return
		// ErrSessionNotFound here, which would leave the pending-recycle intent
		// stuck retrying forever on every watcher tick. Start a fresh session
		// directly instead; 80x24 mirrors Start's own new-session default.
		_, startErr := r.runner.Start(task, r.cfgFn(), 24, 80, false)
		return startErr
	}

	return r.runner.Recycle(task, r.cfgFn(), rows, cols)
}
