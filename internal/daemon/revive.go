package daemon

import (
	"fmt"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
)

// HeraReviveRunner implements hera.ReviveRunner against the daemon's real
// session runner and DB (add-hera-revive). It is the production counterpart
// to internal/hera/revive_test.go's fakeReviveRunner, wired into the daemon's
// hera_revive MCP tool via Daemon.heraReviveRole. See
// openspec/changes/add-hera-revive/design.md D3 for why the TUI's Enter-key
// revive path (internal/tui/heraactions.go) keeps its own inline
// implementation rather than sharing this adapter: it additionally resizes to
// the current pane's dimensions (no such rendering surface exists here) and is
// threaded through tview's QueueUpdateDraw model. Every underlying check this
// adapter performs is nonetheless single-sourced with the TUI's version —
// agent.BlockedOnPrompt, db.ReviveHeraWorkerToInProgress, and
// agent.SessionRunner's KickRerender/StartOrReattach are the same functions
// either way.
type HeraReviveRunner struct {
	database *db.DB
	runner   agent.SessionRunner
	cfgFn    func() config.Config
}

// NewHeraReviveRunner builds the production hera.ReviveRunner.
func NewHeraReviveRunner(database *db.DB, runner agent.SessionRunner, cfgFn func() config.Config) *HeraReviveRunner {
	return &HeraReviveRunner{database: database, runner: runner, cfgFn: cfgFn}
}

var _ hera.ReviveRunner = (*HeraReviveRunner)(nil)

// IsAlive reports whether taskID has a live session at all.
func (r *HeraReviveRunner) IsAlive(taskID string) bool {
	sess := r.runner.Get(taskID)
	return sess != nil && sess.Alive()
}

// IsIdle reports whether taskID's live session is currently idle.
func (r *HeraReviveRunner) IsIdle(taskID string) bool {
	sess := r.runner.Get(taskID)
	return sess != nil && sess.IsIdle()
}

// BlockedOnPrompt reports whether taskID's live session is idle AND parked at
// a user prompt (selection UI overlay or trailing question) — the signature a
// kick must never dismiss. Delegates to agent.BlockedOnPrompt, which reads the
// session's own in-process ring buffer; that read is correct here (unlike a
// TUI running in daemon-client mode) because the daemon process always owns
// the live ring regardless of whether any client has attached a stream.
func (r *HeraReviveRunner) BlockedOnPrompt(taskID string) bool {
	sess := r.runner.Get(taskID)
	if sess == nil || !sess.IsIdle() {
		return false
	}
	return agent.BlockedOnPrompt(sess)
}

// HasPendingRestart reports whether a kick/restart is already queued for
// taskID.
func (r *HeraReviveRunner) HasPendingRestart(taskID string) bool {
	return r.runner.HasPendingRestart(taskID)
}

// KickRerender stops and resumes taskID's live session in place, at its
// existing PTY dimensions — there is no rendering surface to fit here, unlike
// the TUI's Enter-key revive which also resizes to the current pane's width
// (doubling as its own BUG-074 size-drift fix).
func (r *HeraReviveRunner) KickRerender(taskID string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("revive kick: load task %s: %w", taskID, err)
	}
	if task == nil {
		return fmt.Errorf("revive kick: task %s not found", taskID)
	}
	sess := r.runner.Get(taskID)
	if sess == nil {
		return fmt.Errorf("revive kick: no live session for task %s", taskID)
	}
	cols, rows := sess.PTYSize()
	return r.runner.KickRerender(task, r.cfgFn(), uint16(rows), uint16(cols)) //nolint:gosec // bounded by terminal cell count
}

// RestartDead restarts a session with no live process, resuming via
// --session-id when the task carries one — mirrors handleRestartTask /
// handleResumeTask (internal/api/handlers.go), the same daemon-side
// dead-session restart the REST API already exposes.
func (r *HeraReviveRunner) RestartDead(taskID string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("revive restart: load task %s: %w", taskID, err)
	}
	if task == nil {
		return fmt.Errorf("revive restart: task %s not found", taskID)
	}
	cfg := r.cfgFn()
	agent.RefreshResumeSessionID(r.database, task)
	resume := task.SessionID != ""
	sess, _, err := r.runner.StartOrReattach(task, cfg, 24, 80, resume)
	if err != nil {
		return fmt.Errorf("revive restart: start task %s: %w", taskID, err)
	}
	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	return r.database.Update(task)
}
