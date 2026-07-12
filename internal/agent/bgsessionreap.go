package agent

import (
	"context"
	"errors"

	"github.com/drn/argus/internal/claudeagents"
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
// tests; production callers ignore the result.
func reapOrphanedClaudeSessions(taskID, worktreeDir string) []string {
	if worktreeDir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeagents.DefaultTimeout)
	defer cancel()

	sessions, err := listBackgroundSessionsFn(ctx, worktreeDir)
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
		if err := stopBackgroundSessionFn(ctx, s.ID); err != nil {
			uxlog.Log("[bgreap] task=%s claude stop %s failed: %v", taskID, s.ID, err)
			continue
		}
		uxlog.Log("[bgreap] task=%s stopped orphaned claude background session id=%s pid=%d", taskID, s.ID, s.PID)
		stopped = append(stopped, s.ID)
	}
	return stopped
}
