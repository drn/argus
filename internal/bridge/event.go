// Package bridge is the daemon side of the argus bridge: the typed wire
// contract of the Claude Code mod that reports session state as structured
// events, the in-memory per-task state built from those events, and the
// shadow comparator that measures how often screen-reading disagrees with
// them. Phase 1 only observes; nothing here changes a verdict.
package bridge

// Event types the mod posts (internal/bridgemod/mod/argus-bridge).
const (
	TypeSessionStart = "session.start"
	TypeTurnStart    = "turn.start"
	TypeTurnComplete = "turn.complete"
	TypeNeedsInput   = "needs_input"
	TypeCleared      = "cleared"
	TypeMeasure      = "session.measure"
	TypeHeartbeat    = "heartbeat"
)

// Needs-input kinds carried by a needs_input event.
const (
	KindQuestion   = "question"
	KindPlan       = "plan"
	KindPermission = "permission"
)

// Context is the live context-window fill reported by session.measure.
type Context struct {
	Tokens  *int64   `json:"tokens,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
	Window  int64    `json:"window,omitempty"`
}

// RateLimit is one plan-usage window reported by session.measure.
type RateLimit struct {
	Kind        string  `json:"kind"`
	PercentUsed float64 `json:"percent_used"`
	ResetsAt    string  `json:"resets_at,omitempty"`
}

// Event is one bridge event. Seq is monotonic within one Boot (a random id the
// mod mints at register, because a hot reload resets the mod's module state);
// TS is the mod's clock in epoch milliseconds and is informational only — the
// daemon judges liveness by its own receive time.
type Event struct {
	Type      string `json:"type"`
	Seq       int64  `json:"seq"`
	Boot      string `json:"boot"`
	TS        int64  `json:"ts"`
	SessionID string `json:"session_id,omitempty"`

	// session.start
	Model string `json:"model,omitempty"`
	Cwd   string `json:"cwd,omitempty"`

	// turn.start / turn.complete
	TurnID string `json:"turn_id,omitempty"`
	Reason string `json:"reason,omitempty"`

	// needs_input / cleared
	Kind      string `json:"kind,omitempty"`
	Tool      string `json:"tool,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`

	// session.measure
	Context    *Context    `json:"context,omitempty"`
	CostUSD    *float64    `json:"cost_usd,omitempty"`
	RateLimits []RateLimit `json:"rate_limits,omitempty"`
}

// Batch is the request body of POST /api/tasks/{id}/bridge/events.
type Batch struct {
	Events []Event `json:"events"`
}

func knownType(t string) bool {
	switch t {
	case TypeSessionStart, TypeTurnStart, TypeTurnComplete, TypeNeedsInput,
		TypeCleared, TypeMeasure, TypeHeartbeat:
		return true
	}
	return false
}
