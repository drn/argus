package daemon

import (
	"errors"

	"github.com/drn/argus/internal/agent"
)

// ErrNudgeNoSession is returned by runnerNudger.Nudge when the target task has
// no live PTY. Senders treat this as a non-error (best-effort delivery): the
// message is already durably committed in task_messages, so a missing PTY
// just means delivery=queued rather than delivery=nudged.
var ErrNudgeNoSession = errors.New("no live session for nudge target")

// runnerNudger adapts *agent.Runner to mcp.MessageNudger. Lookup is a
// snapshot at call time — if the session exits between snapshot and Write,
// the write errors and we surface that up to the caller.
type runnerNudger struct {
	runner *agent.Runner
}

// Nudge writes a single-line notification into the target task's PTY. Returns
// ErrNudgeNoSession when no live session exists. Any other error comes from
// the PTY write itself (closed pipe, etc.) and is bubbled up so the MCP
// handler can log it.
func (n runnerNudger) Nudge(targetTaskID string, line string) error {
	if n.runner == nil {
		return ErrNudgeNoSession
	}
	sess := n.runner.Get(targetTaskID)
	if sess == nil {
		return ErrNudgeNoSession
	}
	if _, err := sess.WriteInput([]byte(line)); err != nil {
		return err
	}
	return nil
}
