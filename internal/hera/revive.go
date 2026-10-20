package hera

import (
	"fmt"

	"github.com/drn/argus/internal/model"
)

// ReviveOutcome enumerates what a hera_revive attempt actually did, per
// design.md (add-hera-revive). Every path is reported — there is no silent
// no-op.
type ReviveOutcome string

const (
	// ReviveRestartedDead means the role's session had no live process; it was
	// restarted in place (resuming via --session-id when the task carries one).
	ReviveRestartedDead ReviveOutcome = "restarted_dead"
	// ReviveKickedStuck means the role's session was alive, idle, and not
	// blocked on a prompt (the "genuinely stuck" signature); it was kicked
	// (stopped and resumed in place).
	ReviveKickedStuck ReviveOutcome = "kicked_stuck"
	// ReviveSkippedCoordinatorLive means the target is a live coordinator role
	// — presumed operator-interactive, never auto-restarted.
	ReviveSkippedCoordinatorLive ReviveOutcome = "skipped_coordinator_live"
	// ReviveSkippedBusy means the role's session is alive and actively
	// producing output — left untouched.
	ReviveSkippedBusy ReviveOutcome = "skipped_busy"
	// ReviveSkippedBlocked means the role's session is idle but parked at a
	// user prompt (selection UI or trailing question) — left untouched so the
	// pending question is never dismissed.
	ReviveSkippedBlocked ReviveOutcome = "skipped_blocked_on_prompt"
	// ReviveSkippedPending means a kick/restart is already queued for this
	// task — left untouched to avoid a duplicate.
	ReviveSkippedPending ReviveOutcome = "skipped_restart_pending"
	// ReviveSkippedNoSessionID means the session is alive but the task has no
	// session id to resume in place — left untouched.
	ReviveSkippedNoSessionID ReviveOutcome = "skipped_no_session_id"
	// ReviveSkippedAlive means a dead-only revive (ReviveDeadRole, hera_send's
	// auto-revive) found a live session and left it untouched — message
	// delivery itself wakes a live idle agent, and a kick would kill its
	// in-session background jobs (fix-hera-send-no-kick).
	ReviveSkippedAlive ReviveOutcome = "skipped_alive"
)

// ReviveStore is the narrow DB surface ReviveRole needs. Satisfied by the
// real *db.DB.
type ReviveStore interface {
	Get(taskID string) (*model.Task, error)
	// ReviveHeraWorkerToInProgress is the single shared helper (see
	// internal/db/hera.go) that restores a worker-bound task from in_review
	// back to in_progress, unless it's awaiting coordinator close-out.
	ReviveHeraWorkerToInProgress(taskID string) (bool, error)
}

// ReviveRunner is the daemon-side seam for session liveness checks and the
// actual restart/kick mechanics — injected so ReviveRole's gating logic is
// testable without a real PTY. Mirrors RecycleRunner's shape (recycle.go).
type ReviveRunner interface {
	// IsAlive reports whether taskID has a live session at all.
	IsAlive(taskID string) bool
	// IsIdle reports whether taskID's live session is idle (no recent
	// output). Only meaningful when IsAlive is true.
	IsIdle(taskID string) bool
	// BlockedOnPrompt reports whether taskID's live session is idle AND
	// parked at a user prompt. Only meaningful when IsIdle is true.
	BlockedOnPrompt(taskID string) bool
	// HasPendingRestart reports whether a kick/restart is already queued for
	// taskID.
	HasPendingRestart(taskID string) bool
	// KickRerender stops and resumes taskID's live session in place.
	KickRerender(taskID string) error
	// RestartDead starts (or --session-id resumes) a session for a task with
	// no live process.
	RestartDead(taskID string) error
}

// ReviveRole attempts a PULL-revive of the hera role bound to taskID, per
// design.md (add-hera-revive) D3. isCoordinator identifies the TARGET role's
// kind (not the caller's) — a live coordinator is presumed
// operator-interactive and is never auto-restarted, mirroring the TUI's
// Enter-key gate (internal/tui/heraactions.go's heraReattach/
// reviveHeraWorker), whose individual checks (agent.BlockedOnPrompt,
// db.ReviveHeraWorkerToInProgress, SessionRunner.KickRerender/StartOrReattach)
// this function's ReviveRunner/ReviveStore implementations reuse rather than
// reimplement.
//
// A dead session (any role kind, including a coordinator) is always
// restarted. A live session is otherwise gated, in order: coordinator role →
// skip; no session id → skip; a kick already in flight → skip; busy → skip;
// blocked on a prompt → skip; otherwise → kick, then best-effort restore the
// task to in_progress (a restore failure does not fail the kick itself —
// mirrors reviveRestoreInProgress's soft-fail).
func ReviveRole(store ReviveStore, runner ReviveRunner, taskID string, isCoordinator bool) (ReviveOutcome, error) {
	task, outcome, err := restartIfDead(store, runner, taskID)
	if err != nil || outcome != "" {
		return outcome, err
	}

	if isCoordinator {
		return ReviveSkippedCoordinatorLive, nil
	}
	if task.SessionID == "" {
		return ReviveSkippedNoSessionID, nil
	}
	if runner.HasPendingRestart(taskID) {
		return ReviveSkippedPending, nil
	}
	if !runner.IsIdle(taskID) {
		return ReviveSkippedBusy, nil
	}
	if runner.BlockedOnPrompt(taskID) {
		return ReviveSkippedBlocked, nil
	}

	if err := runner.KickRerender(taskID); err != nil {
		return "", fmt.Errorf("revive: kick stuck session for task %s: %w", taskID, err)
	}
	// Best-effort: the kick itself already succeeded; a worker left stranded
	// in in_review (e.g. awaiting close-out) can still be closed manually.
	_, _ = store.ReviveHeraWorkerToInProgress(taskID)
	return ReviveKickedStuck, nil
}

// ReviveDeadRole is the dead-only sibling of ReviveRole, used by hera_send's
// auto-revive (fix-hera-send-no-kick): a dead session is restarted exactly as
// ReviveRole would, and any live session — idle, busy, blocked, or a
// coordinator — is left untouched and reported as ReviveSkippedAlive. A send
// never kicks: "alive + idle + not blocked" is the normal resting state of an
// agent between turns or waiting on a background job, and the agent's own
// shutdown on a kick tears those jobs down.
func ReviveDeadRole(store ReviveStore, runner ReviveRunner, taskID string) (ReviveOutcome, error) {
	_, outcome, err := restartIfDead(store, runner, taskID)
	if err != nil || outcome != "" {
		return outcome, err
	}
	return ReviveSkippedAlive, nil
}

// restartIfDead loads taskID and restarts its session when it has no live
// process, returning ReviveRestartedDead. A live session returns the loaded
// task and an empty outcome so the caller applies its own live-session policy.
func restartIfDead(store ReviveStore, runner ReviveRunner, taskID string) (*model.Task, ReviveOutcome, error) {
	task, err := store.Get(taskID)
	if err != nil {
		return nil, "", fmt.Errorf("revive: load task %s: %w", taskID, err)
	}
	if task == nil {
		return nil, "", fmt.Errorf("revive: task %s not found", taskID)
	}
	if runner.IsAlive(taskID) {
		return task, "", nil
	}
	if err := runner.RestartDead(taskID); err != nil {
		return nil, "", fmt.Errorf("revive: restart dead session for task %s: %w", taskID, err)
	}
	return task, ReviveRestartedDead, nil
}
