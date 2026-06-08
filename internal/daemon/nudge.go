package daemon

import (
	"errors"
	"strings"

	"github.com/drn/argus/internal/notify"
)

// ErrNudgeNoSession is returned by runnerNudger.Nudge when the target task has
// no live PTY and no notifier is wired. Senders treat this as a non-error
// (best-effort delivery): the message is already durably committed in
// task_messages, so a missing PTY just means delivery=queued.
var ErrNudgeNoSession = errors.New("no live session for nudge target")

// runnerNudger adapts *notify.Notifier to mcp.MessageNudger. Nudge registers
// a reliable delivery keyed by deliveryID so the text is submitted (with
// idle+focus gates) exactly once. Cancel tears down a pending delivery when
// the recipient acknowledges the message.
type runnerNudger struct {
	notifier *notify.Notifier
}

// Nudge registers a reliable delivery of line to the target task. deliveryID
// should be the message's DB ID so the delivery can be cancelled on ack.
// Returns ErrNudgeNoSession when no notifier is wired OR when no live session
// exists for the target at registration time — callers use this to report
// delivered="queued" vs delivered="nudged" accurately.
func (n runnerNudger) Nudge(targetTaskID, deliveryID, line string) error {
	if n.notifier == nil {
		return ErrNudgeNoSession
	}
	// Strip outer newlines: the notifier adds Ctrl+U + text + CR itself.
	text := strings.Trim(line, "\n\r")
	n.notifier.ReliableNotify(targetTaskID, text, deliveryID, notify.NotifyOpts{})
	// Return ErrNudgeNoSession when no session is live so the MCP layer can
	// accurately report delivered="queued". The delivery is still registered
	// and will submit when a session appears.
	if !n.notifier.SessionExists(targetTaskID) {
		return ErrNudgeNoSession
	}
	return nil
}

// Cancel tears down a pending reliable delivery for the named message.
// Called when the recipient acknowledges the message (read_at set).
// Safe to call when no delivery is registered (no-op).
func (n runnerNudger) Cancel(targetTaskID, deliveryID string) error {
	if n.notifier == nil {
		return nil
	}
	n.notifier.Cancel(targetTaskID, deliveryID)
	return nil
}
