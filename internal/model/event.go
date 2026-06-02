package model

import (
	"encoding/json"
	"time"
)

// Event is one row in the daemon-wide events ring (PR 2 of the plugin
// substrate). Plugins consume events through the /api/events/stream SSE
// channel; the ring also backs replay on reconnect via the `since` cursor.
//
// Payload travels as raw JSON so each event type can evolve its body without
// requiring schema migrations on the events table. The shape per type is
// documented alongside the EventType* constants below.
type Event struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	At      time.Time       `json:"at"`
	TaskID  string          `json:"task_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Canonical event type strings. Plugins rely on these being stable — changing
// any value here is a breaking change to the plugin contract (would require
// the X-Argus-Plugin-Version major bump described in docs/plugins.md).
const (
	EventTypeTaskCreated       = "task.created"
	EventTypeTaskStatusChanged = "task.status_changed"
	EventTypeTaskCompleted     = "task.completed"
	EventTypeTaskArchived      = "task.archived"
	EventTypeTaskRenamed       = "task.renamed"
	EventTypeTaskForked        = "task.forked"
	EventTypeMessageSent       = "message.sent"
	EventTypeMessageAcked      = "message.acked"
	EventTypeLinkCreated       = "link.created"
	EventTypeLinkRemoved       = "link.removed"
	EventTypeSessionStarted    = "session.started"
	EventTypeSessionExited     = "session.exited"
	EventTypeSessionIdle       = "session.idle"

	// EventTypeSessionNeedsInput fires when a session transitions into or out
	// of the "blocked waiting on the user" state (the red ? in the TUI). One
	// event type carries both edges; the payload's needs_input bool
	// distinguishes enter (true) from clear (false), e.g.
	// {"needs_input":true}. Emitted by the daemon's idle/needs-input watcher
	// (internal/api/push.go) so external consumers render the signal without
	// re-deriving argus's detection heuristic from session logs.
	EventTypeSessionNeedsInput = "session.needs_input"

	// EventTypeResync is synthesised by the SSE handler when a client
	// connects with a `since` cursor older than the oldest retained event.
	// It is never persisted in the events table — clients seeing it should
	// resnapshot the daemon's current state.
	EventTypeResync = "resync"
)
