package daemon

import (
	"fmt"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
)

// heraRecycleRunner implements hera.RecycleRunner against the daemon's real
// session runner and DB (add-coordinator-context-management D5). It is the
// seam recycle_test.go's fakeRecycleRunner stands in for in unit tests — this
// is the concrete, production version wired into the daemon's background
// recycle sweep and (Stage 7) the rail's human-forced keybinding.
type heraRecycleRunner struct {
	database *db.DB
	runner   agent.SessionRunner
	cfgFn    func() config.Config
}

func newHeraRecycleRunner(database *db.DB, runner agent.SessionRunner, cfgFn func() config.Config) *heraRecycleRunner {
	return &heraRecycleRunner{database: database, runner: runner, cfgFn: cfgFn}
}

var _ hera.RecycleRunner = (*heraRecycleRunner)(nil)

// IsIdle reports whether taskID's live session is currently idle. A missing
// session (already exited, or never started) is treated as idle — nothing is
// actively producing output, so a self-service recycle should proceed rather
// than wait forever for a session that no longer exists.
func (r *heraRecycleRunner) IsIdle(taskID string) bool {
	sess := r.runner.Get(taskID)
	return sess == nil || sess.IsIdle()
}

// StopStrayJobs cleans up any Claude Code background job tied to sessionID
// before the caller restarts taskID (design.md Risks: task_stop does not kill
// everything).
func (r *heraRecycleRunner) StopStrayJobs(taskID, sessionID string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("stop stray jobs: load task %s: %w", taskID, err)
	}
	return agent.StopStrayJobs(task, r.cfgFn(), sessionID)
}

// Restart resolves the coordinator role bound to taskID, assembles the fresh
// session's seed prompt (mission + plan-DAG state + handoff note), clears any
// stale SessionID so BuildCmd starts genuinely fresh rather than colliding on
// an already-used UUID, persists both, and hands off to the runner's
// same-task recycle primitive.
func (r *heraRecycleRunner) Restart(taskID string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("recycle restart: load task %s: %w", taskID, err)
	}

	binding, err := r.database.HeraLiveBindingByTask(taskID)
	if err != nil {
		return fmt.Errorf("recycle restart: resolve binding for task %s: %w", taskID, err)
	}

	seedPrompt, err := hera.BuildRecycleSeedPrompt(r.database, binding.RoleID)
	if err != nil {
		return fmt.Errorf("recycle restart: build seed prompt for role %d: %w", binding.RoleID, err)
	}

	// Capture the outgoing session's PTY size before Recycle stops it, so the
	// fresh session opens at the same dimensions rather than falling back to
	// Start's 80x24 default.
	var rows, cols uint16
	if sess := r.runner.Get(taskID); sess != nil {
		c, rw := sess.PTYSize()
		cols, rows = uint16(c), uint16(rw)
	}

	// task.SessionID MUST be cleared before Recycle: BuildCmd's non-resume
	// branch pins any non-empty SessionID via --session-id, and the OLD
	// (already-used) UUID would collide rather than mint a fresh session.
	task.SessionID = ""
	task.Prompt = seedPrompt
	if err := r.database.Update(task); err != nil {
		return fmt.Errorf("recycle restart: persist cleared session id for task %s: %w", taskID, err)
	}

	return r.runner.Recycle(task, r.cfgFn(), rows, cols)
}
