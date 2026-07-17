package agent

import (
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// RefreshResumeSessionID is the RESUME-TIME analog of the daemon's post-exit
// captureSessionIDPostExit hook. Before a Claude-style task is resumed, it
// re-derives the newest transcript UUID for the task's worktree and persists it
// as the task's SessionID, so the resume targets the MOST RECENT in-place
// session rather than the stale first --session-id UUID minted at create.
//
// Why a second capture site is needed: task.SessionID is only ever refreshed to
// the newest worktree transcript by captureSessionIDPostExit, but hera workers
// never reach it — they idle instead of exiting (RollHeraWorkerToReview), and
// supervisor death surfaces as StreamLost, which short-circuits handleSessionExit
// before the recapture. Coordinators reboot a worker's session in-place, so the
// worktree accrues newer transcripts while task.SessionID stays pinned to the
// create-time UUID. Without a resume-time refresh, the next resume loads the
// first/stale conversation and loses all context accrued since.
//
// Claude-only: this is the same gate captureSessionIDPostExit expresses via
// NeedsSessionRecapture (Claude re-mints its UUID on every /clear / in-place
// reboot, so its stored ID goes stale). codex, pi, and opencode resume with a
// stable ID they captured post-exit (--session / capture-style) and MUST be
// byte-identical to today — the transcript scan never runs for them.
//
// No-op — leaving the recorded SessionID intact and NEVER blanking it — when the
// task is nil, has no worktree, has no prior SessionID (first start; never
// fabricate one), the backend is not Claude, the worktree holds no transcript,
// or the newest transcript already equals the recorded ID. On a genuine change
// it mutates task.SessionID in place (so the caller's immediate resume uses it)
// AND persists via a read-modify-write on the row (mirroring the exit hook), so
// callers that do not re-issue an Update still land the fresh ID. Errors are
// logged via uxlog and never fatal.
func RefreshResumeSessionID(database *db.DB, task *model.Task) {
	if database == nil || task == nil || task.Worktree == "" || task.SessionID == "" {
		return
	}
	cfg := database.Config()
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		uxlog.Log("[resume] session recapture skipped: backend resolve failed task=%s: %v", task.ID, err)
		return
	}
	// Claude-only. codex/pi/opencode keep a stable captured ID across resumes; a
	// Claude transcript scan would be wrong for them (see NeedsSessionRecapture).
	if !IsClaudeBackend(backend.Command) {
		return
	}
	sid, err := CaptureClaudeSessionID(task.Worktree)
	if err != nil {
		// No transcript yet (or scan error): keep the existing ID, never blank it.
		uxlog.Log("[resume] session recapture no-op task=%s (keeping %s): %v", task.ID, task.SessionID, err)
		return
	}
	if sid == "" || sid == task.SessionID {
		uxlog.Log("[resume] session unchanged task=%s sid=%s", task.ID, task.SessionID)
		return
	}
	old := task.SessionID
	task.SessionID = sid
	// Re-read the row before writing so we don't clobber concurrent field
	// updates, mirroring captureSessionIDPostExit's read-modify-write.
	t2, err := database.Get(task.ID)
	if err != nil || t2 == nil {
		uxlog.Log("[resume] session persist skipped task=%s (row gone): %v", task.ID, err)
		return
	}
	t2.SessionID = sid
	if uerr := database.Update(t2); uerr != nil {
		uxlog.Log("[resume] session persist failed task=%s err=%v", task.ID, uerr)
		return
	}
	uxlog.Log("[resume] session refreshed task=%s %s -> %s", task.ID, old, sid)
}
