package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// This file implements the two REST endpoints added by
// openspec/changes/add-mac-keybinding-parity (design.md D3): a REST mirror of
// the TUI's ctrl+r Claude-session switcher (internal/tui/app.go's
// openSessionPickerModal / switchSession, backed by internal/claudesession).
// The mac app is a REST-only client with no Go coupling, so it cannot call
// internal/claudesession in-process the way the TUI does — these two
// endpoints are the only way it (or any other REST client) can reach the
// same data and mechanism.
//
// Kept in a dedicated file rather than folded into the already-1800-line
// handlers.go / 4300-line handlers_test.go, per this task's discretion note.

// errNonClaudeBackend marks a task whose resolved backend is Codex, Pi, or
// Opencode. Both endpoints below are Claude-only, matching the TUI's own
// guard.
var errNonClaudeBackend = errors.New("task backend is not Claude")

// claudeOnlyGuard resolves task's backend and rejects anything recognized as
// Codex, Pi, or Opencode — mirroring the TUI's guard exactly (see
// internal/tui/app.go's openSessionPickerModal): same three functions, same
// logic, deliberately a denylist rather than a Claude allowlist so a bare/
// custom backend command (e.g. a test fixture) is treated as Claude-
// compatible, same as the TUI.
func claudeOnlyGuard(task *model.Task, cfg config.Config) error {
	backend, err := agent.ResolveBackend(task, cfg)
	if err != nil {
		return err
	}
	if agent.IsCodexBackend(backend.Command) || agent.IsPiBackend(backend.Command) || agent.IsOpencodeBackend(backend.Command) {
		return errNonClaudeBackend
	}
	return nil
}

// claudeSessionJSON is the wire shape for one entry in
// GET /api/tasks/{id}/claude-sessions. claudesession.Session has no JSON tags
// (Go-default PascalCase field names), so this DTO owns the snake_case wire
// contract instead of marshaling that package's type directly.
type claudeSessionJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Branch    string `json:"branch,omitempty"`
	PRRef     string `json:"pr_ref,omitempty"`
	ModTime   string `json:"mod_time"`
	SizeBytes int64  `json:"size_bytes"`
}

// claudeSessionToJSON converts one claudesession.Session into its wire DTO.
// ModTime is formatted with time.RFC3339, matching taskJSON.CreatedAt's
// convention (t.CreatedAt.Format(time.RFC3339) in handlers.go).
func claudeSessionToJSON(s claudesession.Session) claudeSessionJSON {
	return claudeSessionJSON{
		ID:        s.ID,
		Title:     s.Title,
		Branch:    s.Branch,
		PRRef:     s.PRRef,
		ModTime:   s.ModTime.Format(time.RFC3339),
		SizeBytes: s.SizeBytes,
	}
}

// --- List Claude sessions ---

// handleListClaudeSessions serves GET /api/tasks/{id}/claude-sessions.
// Callable by any authenticated token (master, device, or plugin-scoped) —
// deliberately NOT requireMaster-gated, matching the delta spec's
// "any authenticated token accepted" scenario.
func (s *Server) handleListClaudeSessions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}

	cfg := s.db.Config()
	if err := claudeOnlyGuard(task, cfg); err != nil {
		if errors.Is(err, errNonClaudeBackend) {
			writeErr(w, http.StatusBadRequest, "session listing is Claude-only", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	sessions, err := claudesession.List(task.Worktree)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	// claudesession.List already returns newest-activity-first.
	out := make([]claudeSessionJSON, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, claudeSessionToJSON(sess))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions":           out,
		"current_session_id": task.SessionID,
	})
}

// --- Switch Claude session ---

type switchClaudeSessionReq struct {
	SessionID string `json:"session_id"`
}

// switchClaudeSessionTimeout/switchClaudeSessionPollInterval bound
// performClaudeSessionSwitch's wait for a queued KickRerender restart to
// complete — mirrors the deadline/retryInterval shape of
// internal/daemon/lock.go's acquireSingletonLock. A well-behaved Claude Code
// process exits within milliseconds of SIGTERM; this budget is generous
// enough to absorb BuildCmd/prelaunch work on the restart side too.
const (
	switchClaudeSessionTimeout      = 10 * time.Second
	switchClaudeSessionPollInterval = 25 * time.Millisecond
)

// handleSwitchClaudeSession serves POST /api/tasks/{id}/claude-session.
// Callable by any authenticated token, same as handleListClaudeSessions.
func (s *Server) handleSwitchClaudeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}

	cfg := s.db.Config()
	if err := claudeOnlyGuard(task, cfg); err != nil {
		if errors.Is(err, errNonClaudeBackend) {
			writeErr(w, http.StatusBadRequest, "session switching is Claude-only", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	var req switchClaudeSessionReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "session_id is required", nil)
		return
	}

	if task.SessionID == req.SessionID {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unchanged"})
		return
	}

	// Persist the new SessionID BEFORE stopping/restarting: both restart
	// paths inside performClaudeSessionSwitch (KickRerender's queued restart,
	// or a fresh StartOrReattach) read task.SessionID off this exact
	// *model.Task to build --resume <id>. This handler deliberately never
	// calls agent.RefreshResumeSessionID anywhere in this path — that helper
	// re-derives the newest worktree transcript by mtime, which would
	// silently overwrite the caller's explicit choice (the entire point of
	// this endpoint). See design.md D3 and handleRestartTask's contrasting
	// use of RefreshResumeSessionID for the "resume the latest" case.
	task.SessionID = req.SessionID
	if err := s.db.Update(task); err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	fn := s.performClaudeSessionSwitchFn
	if fn == nil {
		fn = s.performClaudeSessionSwitch
	}
	pid, err := fn(task, cfg)
	if err != nil {
		uxlog.Log("[api] claude session switch: task=%s failed: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	uxlog.Log("[api] claude session switch: task=%s -> session=%s pid=%d", id, req.SessionID, pid)
	writeJSON(w, http.StatusOK, map[string]any{"status": "switched", "pid": pid})
}

// performClaudeSessionSwitch stops any live session for task.ID and restarts
// it resuming task.SessionID (already persisted by the caller above), or
// starts fresh if no live session exists. Returns the resulting session's
// PID.
//
// Synchronization mechanism (why this is safe):
//
// internal/agent.Runner.Stop only sends SIGTERM to the PTY's process and
// returns immediately — the old session is NOT removed from the runner's
// session map until its own exit-watcher goroutine (spawned inside
// Runner.Start when the session was originally started) observes the actual
// process exit and runs its cleanup, which happens on a separate goroutine
// with no ordering guarantee relative to this HTTP handler. Calling Start (or
// StartOrReattach) again immediately after Stop races that cleanup and can
// fail with "session already exists for task X" — see the runner-ordering
// invariants in context/knowledge/gotchas/daemon-rpc.md ("do not reorder"
// bullet, and the pendingRestart doc comment in internal/agent/runner.go).
//
// Runner.KickRerender is the codebase's existing, already-tested primitive
// for exactly this "stop, then once the process is truly gone, restart in
// place" sequence — it queues a pendingRestart entry, sends the SIGTERM, and
// the ACTUAL restart happens from inside the SAME exit-watcher goroutine that
// owns the session-map delete, strictly after that delete. There is no race
// window because we never call Start ourselves — the runner does, at a point
// it alone controls. This is also spelled out as a hard rule on the
// SessionRunner interface (internal/agent/iface.go): "The runner owns the
// pendingRestart bookkeeping... callers MUST NOT emulate it with Stop+Start."
// KickRerender's resume=true restart uses the exact *model.Task pointer this
// function was given, so it resumes the just-persisted new SessionID via
// --resume, not the old one.
//
// The one gap versus this endpoint's synchronous "return the new pid" REST
// contract is that KickRerender itself returns before the queued restart
// completes. This function bridges that gap with a bounded poll on
// Runner.HasPendingRestart / Runner.Get — the exact pair of lock-free,
// already-public Runner accessors the codebase itself exposes over REST at
// GET /api/sessions/{id}/pending-restart for clients to observe the same
// asynchronous completion (see handleHasPendingRestart in handlers.go).
// Polling here is the server doing, in one blocking call, what a REST client
// would otherwise have to do itself across several requests; the interval/
// timeout constants mirror the shape of internal/daemon/lock.go's
// acquireSingletonLock. If the deadline is hit (an unresponsive or
// grandchild-holding agent — see session.go's ptmxDrainTimeout escape hatch,
// itself only 5s), the switch is reported as a failure (500) rather than
// hanging the request indefinitely.
func (s *Server) performClaudeSessionSwitch(task *model.Task, cfg config.Config) (int, error) {
	sess := s.runner.Get(task.ID)
	if sess == nil || !sess.Alive() {
		newSess, _, err := s.runner.StartOrReattach(task, cfg, 24, 80, true)
		if err != nil {
			return 0, err
		}
		task.SetStatus(model.StatusInProgress)
		task.AgentPID = newSess.PID()
		if err := s.db.Update(task); err != nil {
			return 0, err
		}
		return newSess.PID(), nil
	}

	// Preserve the live session's current PTY size across the restart —
	// this is a session switch, not a resize.
	cols, rows := sess.PTYSize()
	if err := s.runner.KickRerender(task, cfg, uint16(rows), uint16(cols)); err != nil {
		return 0, err
	}

	deadline := time.Now().Add(switchClaudeSessionTimeout)
	for s.runner.HasPendingRestart(task.ID) {
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("claude session switch: timed out waiting for task %s to restart", task.ID)
		}
		time.Sleep(switchClaudeSessionPollInterval)
	}

	newSess := s.runner.Get(task.ID)
	if newSess == nil {
		return 0, fmt.Errorf("claude session switch: restart of task %s did not produce a live session", task.ID)
	}
	task.SetStatus(model.StatusInProgress)
	task.AgentPID = newSess.PID()
	if err := s.db.Update(task); err != nil {
		return 0, err
	}
	return newSess.PID(), nil
}
