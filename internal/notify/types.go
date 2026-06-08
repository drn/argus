// Package notify provides the reliable pane-delivery service. It injects
// text into a task's PTY and submits it exactly once (Ctrl+U + text + CR)
// as soon as the session is idle and no human is focused on that pane.
package notify

import (
	"time"
)

// defaultDeadlineMS is the default delivery deadline (5 minutes).
const defaultDeadlineMS = 5 * 60 * 1000

// maxSubmittedPerTask is the LRU cap on remembered submitted deliveryIDs
// per task. Entries beyond this limit are evicted FIFO. Re-posting an
// evicted ID would re-inject; in practice a task never accumulates this many.
const maxSubmittedPerTask = 1000

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
}

// SessionHandleIface is the subset of agent.SessionHandle that the Notifier
// requires. It is satisfied by *agent.Session, *client.RemoteSession, and any
// test fake. Keeping this narrow avoids importing the agent package here and
// makes test fakes much simpler.
type SessionHandleIface interface {
	IsIdle() bool
	WriteInput(p []byte) (int, error)
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
