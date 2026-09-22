// Package notify provides the reliable pane-delivery service. It injects text
// into a task's PTY once the visible composer is safe, then verifies standalone
// CR submission from recipient-side output. Idle and focus are conservative
// fallbacks for terminal layouts whose composer cannot be identified.
package notify

import (
	"time"

	"github.com/drn/argus/internal/app/agentview"
)

// defaultDeadlineMS is the default delivery deadline (5 minutes).
const defaultDeadlineMS = 5 * 60 * 1000

// maxSubmittedPerTask is the LRU cap on remembered submitted deliveryIDs
// per task. Entries beyond this limit are evicted FIFO. Re-posting an
// evicted ID would re-inject; in practice a task never accumulates this many.
const maxSubmittedPerTask = 1000

const (
	// maxTotalSubmitAttempts bounds CR writes over the entire lifetime of one
	// delivery, not merely one Reconcile pass. Three full acknowledgment windows
	// allow a slow terminal to recover while preventing a detection failure from
	// generating unbounded real agent turns.
	maxTotalSubmitAttempts = 9

	// maxUnconfirmedStableClearAttempts bounds Ctrl+U clear checks for a
	// captured stable draft. A persistent false-positive composer frame must
	// fall back to annotated preservation rather than consume the deadline.
	maxUnconfirmedStableClearAttempts = 3

	// outputPollInterval bounds how quickly reliable-notify polls the lock-free
	// session output counter while waiting for recipient-side evidence.
	outputPollInterval = 10 * time.Millisecond

	// textSettleTimeout is the longest one delivery waits for the recipient to
	// consume and redraw injected text before attempting Enter anyway.
	textSettleTimeout = 5 * time.Second

	// textQuietWindow separates the final composer redraw from the standalone
	// Enter write. Unlike the old blind delay, it starts after observed output.
	textQuietWindow = 100 * time.Millisecond

	// draftStabilityWindow is the minimum time non-notice composer content
	// must remain byte-identical before reliable-notify treats it as abandoned
	// rather than actively typed.
	draftStabilityWindow = 5 * time.Second
)

const abandonedDraftAnnotation = "Argus notice: the preceding input was left unsubmitted. Do not act on it. Process only the notice below."

// submitAckTimeouts are increasing acknowledgment windows for standalone CR
// attempts. For identifiable composers, a changed rendered draft is the
// acknowledgment that Enter was consumed; PTY output alone is only a fallback
// for unsupported layouts.
var submitAckTimeouts = [...]time.Duration{
	500 * time.Millisecond,
	1500 * time.Millisecond,
	3 * time.Second,
}

// NotifyOpts controls optional parameters for ReliableNotify.
type NotifyOpts struct {
	// DeadlineMS is the maximum milliseconds to wait for a safe submit window.
	// Zero or negative means use defaultDeadlineMS (5 minutes).
	DeadlineMS int64
}

// delivery holds state for one pending reliable delivery.
type delivery struct {
	taskID     string
	text       string
	deliveryID string
	deadline   time.Time
	cancelCh   chan struct{}

	observedDraft   string
	draftObservedAt time.Time
	submitAttempts  int
	// unconfirmedStableClearAttempts counts consecutive reconcile passes where
	// Ctrl+U could not visibly clear the captured stable draft. The bounded
	// fallback preserves delivery when a non-editable frame is misclassified.
	unconfirmedStableClearAttempts int
	// restoreDraft is a real stable composer draft captured before it was
	// cleared for a clean notice submission. It survives CR-only retries so an
	// eventually acknowledged retry restores the original draft exactly once.
	restoreDraft string
}

// SessionHandleIface is the subset of agent.SessionHandle that the Notifier
// requires. It is satisfied by *agent.Session, *client.RemoteSession, and any
// test fake. Keeping this narrow avoids importing the agent package here and
// makes test fakes much simpler.
type SessionHandleIface interface {
	IsIdle() bool
	RecentOutputTail(n int) []byte
	// TotalWritten is the monotonic count of PTY output bytes observed for the
	// session. Reliable-notify uses it as submit acknowledgment evidence.
	TotalWritten() uint64
	PTYSize() (cols, rows int)
	// WriteInput injects the delivery as SYSTEM-origin input: it advances the
	// agent's work cycle but NOT the user-input timestamp, so a delivered
	// hera/task message never masquerades as the user answering a prompt and
	// never clears the needs-input "(?)" flag (BUG-034). Notify always calls
	// this with agentview.OriginSystem — never agentview.OriginUser.
	WriteInput(p []byte, origin agentview.InputOrigin) (int, error)
}

// RunnerIface is the subset of agent.SessionProvider needed by the notifier.
// Returning SessionHandleIface (not the full agent.SessionHandle) makes both
// test fakes and daemon-client wrappers trivial to write.
type RunnerIface interface {
	Get(taskID string) SessionHandleIface
}

// FocusReader is the read-only view of FocusTracker used by Notifier.
type FocusReader interface {
	IsFocused(taskID string) bool
}

// DeliveryState describes the result of a ReliableNotify or REST call.
type DeliveryState string

const (
	StateSubmitted DeliveryState = "submitted"
	StatePending   DeliveryState = "pending"
)
