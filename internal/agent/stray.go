package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// StopStrayJobs terminates any Claude Code background job (a
// `run_in_background` sub-agent job, tracked by Claude Code's own `claude
// agents` registry rather than argus's PTY) that survives independently of a
// session's primary process. This addresses the documented incident where
// `task_stop`/killing the PTY does not kill everything: a stray background
// job can outlive the session it was spawned from and hold a worktree-write
// lock, causing an EPERM or a resume collision for whatever starts next in
// that worktree (design.md Risks, add-coordinator-context-management).
//
// Best-effort by design: called immediately before a restart, so a failure
// here must not block the restart — a residual stray job is a smaller
// problem than a coordinator permanently unable to recycle. No-op (nil,
// no side effects) when the task's backend isn't Claude, or when sessionID
// is empty (nothing to match against).
func StopStrayJobs(task *model.Task, cfg config.Config, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		slog.Warn("[agent] stray-job cleanup: resolve backend failed", "task", task.ID, "err", err)
		return nil
	}
	if !IsClaudeBackend(backend.Command) {
		return nil // stray sub-agent jobs are a Claude Code-specific concept
	}
	claudeBin := claudeBinaryFromCommand(backend.Command)

	out, err := exec.Command(claudeBin, "agents", "--json").Output() //nolint:gosec // claudeBin resolved from configured backend command, not user input
	if err != nil {
		slog.Warn("[agent] stray-job cleanup: `claude agents --json` failed, skipping", "task", task.ID, "err", err)
		return nil
	}

	ids, err := strayJobIDsForSession(out, sessionID)
	if err != nil {
		slog.Warn("[agent] stray-job cleanup: parse `claude agents --json` output failed, skipping", "task", task.ID, "err", err)
		return nil
	}

	for _, id := range ids {
		if err := exec.Command(claudeBin, "stop", id).Run(); err != nil { //nolint:gosec // claudeBin resolved from configured backend command; id comes from claude's own registry
			slog.Warn("[agent] stray-job cleanup: `claude stop` failed", "task", task.ID, "job_id", id, "err", err)
			continue
		}
		slog.Info("[agent] stray-job cleanup: stopped background job", "task", task.ID, "session", sessionID, "job_id", id)
	}
	return nil
}

// claudeBinaryFromCommand extracts the resolved claude executable (bare name
// or absolute path) from a backend command string, so stray-job cleanup shells
// out to the SAME binary BuildCmd would use rather than assuming "claude" is
// on PATH.
func claudeBinaryFromCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "claude"
	}
	return fields[0]
}

// strayJobIDsForSession parses `claude agents --json` output and returns the
// job IDs tied to sessionID. Tolerant of key-name variants (session_id,
// sessionId, session) since the exact schema isn't pinned by any spec this
// codebase controls — degrades to "no matches" rather than erroring on an
// unrecognized shape, matching the caller's best-effort contract.
func strayJobIDsForSession(data []byte, sessionID string) ([]string, error) {
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("strayJobIDsForSession: %w", err)
	}

	var ids []string
	for _, e := range entries {
		if !entrySessionMatches(e, sessionID) {
			continue
		}
		if id, ok := stringField(e, "id", "job_id", "jobId"); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func entrySessionMatches(e map[string]any, sessionID string) bool {
	got, ok := stringField(e, "session_id", "sessionId", "session")
	return ok && got == sessionID
}

func stringField(e map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := e[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}
