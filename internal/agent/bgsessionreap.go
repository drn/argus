package agent

import (
	"context"
	"errors"

	"github.com/drn/argus/internal/claudeagents"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// listBackgroundSessionsFn / stopBackgroundSessionFn are test seams mirroring
// autoRenameFn — tests swap them so they don't need a real claude binary.
var (
	listBackgroundSessionsFn = claudeagents.List
	stopBackgroundSessionFn  = claudeagents.Stop
)

// reapOrphanedClaudeSessions looks for Claude Code background sessions —
// detached to Claude Code's own per-user supervisor via /bg, /background, or
// a literal Ctrl+Z reaching the PTY (see
// context/knowledge/gotchas/daemon-rpc.md, "Claude Code's own
// background-session supervisor") — whose working directory is this task's
// worktree, and stops any still-alive ones. Argus's own SIGTERM
// (Session.Stop) can never reach such a session: it has already left argus's
// process tree entirely. Only claude stop <id> can.
//
// Fire-and-forget: called from a goroutine in Runner.Stop so a claude CLI
// round-trip never adds latency to a stop request. Every failure is logged
// and swallowed — a missing/older claude CLI, or nothing to reap, are both
// the overwhelmingly common, harmless case. Returns the ids stopped, for
// tests; production callers ignore the result. configDir is the Claude config
// dir the session was spawned under ("" = default); Claude Code's background
// registry is per config dir, so a wrong dir finds nothing.
func reapOrphanedClaudeSessions(taskID, worktreeDir, configDir string) []string {
	return reapBackgroundSessions(taskID, worktreeDir, "", configDir)
}

// reapBackgroundSessionForResume stops the Claude Code background session, if
// any, that is holding the task's conversation — Claude Code refuses
// `--resume <id>` with "That session is running in the background" while it
// is alive. Runs synchronously before a Claude resume so the launch cannot
// race the stop; scoped to the task's own session id so an unrelated
// background session in the same worktree is left alone. Fails open: any
// error is logged and the resume proceeds as it would have.
func reapBackgroundSessionForResume(task *model.Task, cfg config.Config) []string {
	if task.SessionID == "" || task.Worktree == "" {
		return nil
	}
	backend, err := ResolveBackend(task, cfg)
	if err != nil || !IsClaudeBackend(backend.Command) {
		return nil
	}
	configDir, explicit, err := cfg.ClaudeConfigDir(task.Account)
	if err != nil {
		uxlog.Log("[bgreap] task=%s resume reap skipped: account: %v", task.ID, err)
		return nil
	}
	if !explicit {
		configDir = ""
	}
	return reapBackgroundSessions(task.ID, task.Worktree, task.SessionID, configDir)
}

// reapBackgroundSessions is the shared list-filter-stop body. A non-empty
// sessionID restricts the stop to the background session hosting that
// conversation.
func reapBackgroundSessions(taskID, worktreeDir, sessionID, configDir string) []string {
	if worktreeDir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeagents.DefaultTimeout)
	defer cancel()

	sessions, err := listBackgroundSessionsFn(ctx, worktreeDir, configDir)
	if err != nil {
		if !errors.Is(err, claudeagents.ErrUnavailable) {
			uxlog.Log("[bgreap] task=%s list failed: %v", taskID, err)
		}
		return nil
	}

	var stopped []string
	for _, s := range sessions {
		if !s.Backgrounded() || !s.Alive() {
			continue
		}
		if sessionID != "" && s.SessionID != sessionID {
			continue
		}
		if err := stopBackgroundSessionFn(ctx, s.ID, configDir); err != nil {
			uxlog.Log("[bgreap] task=%s claude stop %s failed: %v", taskID, s.ID, err)
			continue
		}
		uxlog.Log("[bgreap] task=%s stopped orphaned claude background session id=%s pid=%d", taskID, s.ID, s.PID)
		stopped = append(stopped, s.ID)
	}
	return stopped
}

// claudeConfigDir returns the CLAUDE_CONFIG_DIR this session's process was
// spawned with, or "" for the default account.
func (s *Session) claudeConfigDir() string {
	if s == nil || s.Cmd == nil {
		return ""
	}
	return claudeConfigDirFromEnv(s.Cmd.Env)
}
