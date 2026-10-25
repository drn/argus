package bridge

import (
	"sync"
	"time"
)

// LiveWindow is how long after its newest event a task's bridge still counts
// as live. The mod heartbeats every ~15s, so this tolerates two missed beats.
const LiveWindow = 45 * time.Second

// pruneAfter drops state for tasks that have been silent this long.
const pruneAfter = time.Hour

// State is the latest bridge-reported state of one task.
type State struct {
	Generation    string    `json:"generation,omitempty"`
	LastMeasureAt time.Time `json:"last_measure_at,omitempty"`
	TaskID        string    `json:"task_id"`
	SessionID     string    `json:"session_id,omitempty"`
	Model         string    `json:"model,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`

	Busy           bool   `json:"busy"`
	NeedsInput     bool   `json:"needs_input"`
	NeedsInputKind string `json:"needs_input_kind,omitempty"`
	// NeedsInputConfidence is ConfidenceExact, ConfidencePending or
	// ConfidencePendingOrRunning (older mods).
	NeedsInputConfidence string `json:"needs_input_confidence,omitempty"`

	Context    *Context    `json:"context,omitempty"`
	CostUSD    *float64    `json:"cost_usd,omitempty"`
	RateLimits []RateLimit `json:"rate_limits,omitempty"`

	Boot             string    `json:"boot,omitempty"`
	LastSeq          int64     `json:"last_seq"`
	Events           int64     `json:"events"`
	FirstEventAt     time.Time `json:"first_event_at"`
	LastEventAt      time.Time `json:"last_event_at"`
	LastHeartbeatAt  time.Time `json:"last_heartbeat_at,omitempty"`
	LastTransitionAt time.Time `json:"last_transition_at,omitempty"`
}

// Hub keeps the latest bridge State per task id, in memory only: a daemon
// restart starts empty and the mod's next heartbeat/turn repopulates it.
type Hub struct {
	mu      sync.Mutex
	states  map[string]*State
	retired map[string]map[string]bool
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{states: make(map[string]*State), retired: make(map[string]map[string]bool)}
}

// Ingest applies events (in order) to the task's state at receive time now and
// returns how many were accepted. Within one Boot an event whose Seq is not
// above the last accepted one is dropped (duplicate or out of order); a new
// Boot is a fresh baseline. Unknown event types are rejected, never applied,
// so a newer mod talking to an older daemon degrades quietly.
func (h *Hub) Ingest(taskID string, events []Event, now time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[taskID]
	accepted := 0
	for _, ev := range events {
		if h.retired[taskID][ev.Boot] {
			continue
		}
		if !knownType(ev.Type) {
			continue
		}
		if st != nil && ev.Boot == st.Boot && ev.Seq <= st.LastSeq {
			continue
		}
		if st == nil {
			st = &State{TaskID: taskID, FirstEventAt: now}
			h.states[taskID] = st
		} else if ev.Boot != st.Boot {
			// A new boot is a restarted or hot-reloaded mod: whatever turn or
			// dialog the previous one had open is unknowable now, so a stopped
			// or restarted mid-turn session must not come back live still busy.
			st.Busy, st.LastTransitionAt = false, now
			st.NeedsInput, st.NeedsInputKind, st.NeedsInputConfidence = false, "", ""
		}
		if st.Events > 0 && ev.Boot != st.Boot {
			if h.retired[taskID] == nil {
				h.retired[taskID] = make(map[string]bool)
			}
			h.retired[taskID][st.Boot] = true
		}
		apply(st, ev, now)
		accepted++
	}
	return accepted
}

func apply(st *State, ev Event, now time.Time) {
	if (ev.SessionID != "" && st.SessionID != "" && ev.SessionID != st.SessionID) || ev.Generation != st.Generation {
		st.Context, st.CostUSD, st.RateLimits = nil, nil, nil
		st.LastMeasureAt = time.Time{}
		st.Busy, st.NeedsInput, st.NeedsInputKind = false, false, ""
		st.NeedsInputConfidence = ""
	}
	if ev.Generation != st.Generation {
		st.SessionID = ""
	}
	st.Generation = ev.Generation
	st.Boot = ev.Boot
	st.LastSeq = ev.Seq
	st.Events++
	st.LastEventAt = now
	if ev.SessionID != "" {
		st.SessionID = ev.SessionID
	}
	switch ev.Type {
	case TypeSessionStart:
		if ev.Model != "" {
			st.Model = ev.Model
		}
		if ev.Cwd != "" {
			st.Cwd = ev.Cwd
		}
	case TypeTurnStart:
		if !st.Busy {
			st.LastTransitionAt = now
		}
		st.Busy = true
	case TypeTurnComplete:
		if st.Busy {
			st.LastTransitionAt = now
		}
		st.Busy = false
		// A finished turn leaves no dialog pending (an interrupt can end a
		// turn without a tool.call post reaching the mod).
		st.NeedsInput, st.NeedsInputKind, st.NeedsInputConfidence = false, "", ""
	case TypeNeedsInput:
		if !st.NeedsInput {
			st.LastTransitionAt = now
		}
		st.NeedsInput, st.NeedsInputKind, st.NeedsInputConfidence = true, ev.Kind, ev.Confidence
	case TypeCleared:
		if st.NeedsInput {
			st.LastTransitionAt = now
		}
		st.NeedsInput, st.NeedsInputKind, st.NeedsInputConfidence = false, "", ""
	case TypeMeasure:
		st.LastMeasureAt = now
		if ev.Context != nil {
			c := *ev.Context
			st.Context = &c
		}
		if ev.CostUSD != nil {
			v := *ev.CostUSD
			st.CostUSD = &v
		}
		st.RateLimits = append([]RateLimit(nil), ev.RateLimits...)
	case TypeHeartbeat:
		if ev.Busy != nil {
			st.Busy = *ev.Busy
		}
		if ev.NeedsInput != nil {
			st.NeedsInput = *ev.NeedsInput
			st.NeedsInputKind = ev.Kind
			st.NeedsInputConfidence = ev.Confidence
		}
		st.LastHeartbeatAt = now
	}
}

// Authority requires a report from this exact process launch. A reused
// transcript ID alone cannot distinguish a restart from the previous process.
func (h *Hub) Authority(taskID, generation string, now time.Time) (State, bool) {
	st, ok := h.Live(taskID, now)
	return st, ok && now.Sub(st.LastEventAt) < LiveWindow && generation != "" && st.Generation == generation && st.SessionID != ""
}

// Get returns a copy of the task's state, live or not.
func (h *Hub) Get(taskID string) (State, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[taskID]
	if st == nil {
		return State{}, false
	}
	return copyState(st), true
}

// Live is Get restricted to a bridge whose newest event is within LiveWindow of
// now. A task that never reported, or went silent, is not live.
func (h *Hub) Live(taskID string, now time.Time) (State, bool) {
	st, ok := h.Get(taskID)
	if !ok || now.Sub(st.LastEventAt) > LiveWindow {
		return State{}, false
	}
	return st, true
}

// Len reports how many tasks currently have bridge state.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.states)
}

// Prune drops state for tasks silent for over an hour, bounding memory for
// sessions that ended without the daemon being told.
func (h *Hub) Prune(now time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for id, st := range h.states {
		if now.Sub(st.LastEventAt) > pruneAfter {
			delete(h.states, id)
			delete(h.retired, id)
			n++
		}
	}
	return n
}

func copyState(st *State) State {
	out := *st
	if st.Context != nil {
		c := *st.Context
		out.Context = &c
	}
	if st.CostUSD != nil {
		v := *st.CostUSD
		out.CostUSD = &v
	}
	out.RateLimits = append([]RateLimit(nil), st.RateLimits...)
	return out
}
