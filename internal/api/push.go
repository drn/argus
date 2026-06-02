package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// pushSubscribeReq matches the W3C PushSubscription serialized shape.
type pushSubscribeReq struct {
	Label    string `json:"label"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handleVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "push not available"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.push.PublicKey()})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "push not available"})
		return
	}
	var req pushSubscribeReq
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint and keys required"})
		return
	}
	id, err := s.db.AddPushSubscription(db.PushSubscription{
		Label:    req.Label,
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	})
	if err != nil {
		uxlog.Log("[push] subscribe failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[push] subscribed id=%d label=%q", id, req.Label)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handlePushList(w http.ResponseWriter, r *http.Request) {
	subs, err := s.db.PushSubscriptions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type subView struct {
		ID        int64  `json:"id"`
		Label     string `json:"label"`
		Endpoint  string `json:"endpoint_masked"`
		CreatedAt int64  `json:"created_at"`
	}
	out := make([]subView, 0, len(subs))
	for _, sub := range subs {
		ep := sub.Endpoint
		if len(ep) > 40 {
			ep = ep[:25] + "…" + ep[len(ep)-12:]
		}
		out = append(out, subView{
			ID:        sub.ID,
			Label:     sub.Label,
			Endpoint:  ep,
			CreatedAt: sub.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := s.db.DeletePushSubscription(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": id})
}

// handlePushTest sends a test notification to all registered devices.
// Useful for verifying the subscribe flow worked end-to-end. Master-only —
// without this guard, any device token holder could spam every registered
// device.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	if s.push == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "push not available"})
		return
	}
	uxlog.Log("[push] test push triggered")
	s.push.Notify("", "Argus test", "Push notifications are working", "")
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// idleWatcherState carries the per-task bookkeeping across idleWatcher ticks.
// Pulled out as a struct so idleWatcherTick can be exercised in unit tests
// without spinning up a real ticker.
type idleWatcherState struct {
	idleNow       map[string]bool      // taskID -> last seen idle?
	seenBefore    map[string]bool      // taskID -> have we observed this session on a prior tick?
	pushedAt      map[string]time.Time // taskID -> wall-clock time we last fired an idle push
	needsInputNow map[string]bool      // taskID -> last seen blocked-on-user-input?
}

func newIdleWatcherState() *idleWatcherState {
	return &idleWatcherState{
		idleNow:       make(map[string]bool),
		seenBefore:    make(map[string]bool),
		pushedAt:      make(map[string]time.Time),
		needsInputNow: make(map[string]bool),
	}
}

// needsInputScanBytes is how many bytes of each session's recent PTY output
// the watcher feeds to agent.DetectNeedsInput. DetectNeedsInput truncates to
// its own tail window internally; this is the generous upper bound read from
// the ring. Matches the TUI's detectNeedsInputTailBytes.
const needsInputScanBytes = 16 * 1024

// computeNeedsInput returns the set of task IDs whose recent PTY output
// indicates the agent is blocked waiting on the user, reusing the shared
// agent.DetectNeedsInput heuristic. It mirrors the TUI's detection
// (internal/tui/app.go detectNeedsInputSticky) so the daemon-published signal
// matches what the TUI renders:
//
//   - Detection is gated on idleness — a still-streaming agent that flashes
//     the marker text transiently is not blocked.
//   - A sticky carry-forward pass re-checks previously-flagged tasks that
//     dropped out of idleIDs this tick. Claude's prompt UI emits periodic
//     animation bytes (cursor blink, spinner) that briefly kick the session
//     out of the idle set; without this the flag (and its SSE events) would
//     oscillate. A sticky entry clears only when the marker is gone from the
//     tail or the session is no longer running.
//
// tailOf returns the recent output tail for a task (nil if unavailable);
// injected so the watcher reads the live session ring while tests supply
// canned bytes. agent.DetectNeedsInput treats nil/empty as "not blocked".
func computeNeedsInput(idleIDs, runningIDs, prev []string, tailOf func(string) []byte) []string {
	out := make([]string, 0, len(idleIDs))
	seen := make(map[string]bool, len(idleIDs))
	for _, id := range idleIDs {
		if seen[id] {
			continue
		}
		if agent.DetectNeedsInput(tailOf(id)) {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(prev) == 0 {
		return out
	}
	runningSet := make(map[string]bool, len(runningIDs))
	for _, id := range runningIDs {
		runningSet[id] = true
	}
	for _, id := range prev {
		if seen[id] || !runningSet[id] {
			continue
		}
		if agent.DetectNeedsInput(tailOf(id)) {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// idleWatcher periodically polls all running sessions and fires
//   - a session.idle event (plugin substrate, PR 2) on every busy→idle
//     transition, regardless of whether push is enabled, and
//   - a Web Push notification on the same transition when push is enabled
//     AND the per-task cycle gate allows it (see shouldFireIdlePush).
//
// 5s tick is the coarsest cadence that still feels responsive in the PWA.
// Exits when s.stopCh is closed (Server.Shutdown).
//
// Single-goroutine: state is only touched here so no mutex is needed.
func (s *Server) idleWatcher() {
	state := newIdleWatcherState()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-tick.C:
		}
		s.idleWatcherTick(state)
	}
}

// idleTransitioned reports whether (id, isIdle) is a busy→idle transition
// relative to the prior tick. Used for session.idle event emission, which
// fires regardless of push state. Does NOT mutate the state — the state
// update happens inside the subsequent shouldFireIdlePush call.
//
// Returns false on the first observation of a session (so a watcher that
// starts with already-idle sessions doesn't fire spurious events on boot)
// and on idle→idle / busy→busy steady states.
func idleTransitioned(state *idleWatcherState, id string, isIdle bool) bool {
	if !state.seenBefore[id] {
		return false
	}
	return isIdle && !state.idleNow[id]
}

// shouldFireIdlePush applies one observation to the per-task state and
// returns whether the watcher should fire a push for an idle transition.
// Deterministic and I/O-free (the only side effect is mutating the passed
// state) so the firing logic can be unit-tested without wiring up a real
// runner + session + db. The gates run in this order:
//
// Visibility gates — must pass before any input check matters:
//   - First observation of a session never fires: prevents spurious push
//     when an already-idle session enters the watcher's view (e.g. fresh
//     idleWatcher start with running sessions present).
//   - Only busy→idle transitions fire (idle→idle and busy→busy are silent).
//
// Input-presence gate — unconditional suppression on no-input sessions:
//   - lastInputAt zero → no fire. A session that auto-started on agent-
//     view entry boots, goes idle waiting for the user's first prompt, and
//     would otherwise nag with a push for work the user hasn't kicked off.
//     lastInputAt is zero until the first WriteInput on the live session.
//
// Cycle-level gate — conditional suppression on already-notified work:
//   - One push per work cycle: after a push, no further pushes fire for the
//     same task until input arrives via WriteInput. lastInputAt is the
//     session's input timestamp; a push is suppressed if a previous push
//     already covered this idle period (no input since). This is the
//     primary defence against repeat pushes for a stale long-idle agent
//     whose incidental output (heartbeats, cursor redraws) keeps flipping
//     IsIdle false→true.
func shouldFireIdlePush(state *idleWatcherState, id string, isIdle bool, lastInputAt time.Time, now time.Time) bool {
	// Visibility gates.
	if !state.seenBefore[id] {
		state.seenBefore[id] = true
		state.idleNow[id] = isIdle
		return false
	}
	wasIdle := state.idleNow[id]
	state.idleNow[id] = isIdle
	if !isIdle || wasIdle {
		return false
	}
	// Input-presence gate.
	if lastInputAt.IsZero() {
		return false
	}
	// Cycle-level gate.
	if pushedAt, ok := state.pushedAt[id]; ok && !lastInputAt.After(pushedAt) {
		// Already pushed for this work cycle and no input has arrived since.
		// The transition is just an output blip on a stale idle session.
		return false
	}
	state.pushedAt[id] = now
	return true
}

// idleWatcherTick runs one iteration of the idle-watch loop. Extracted from
// idleWatcher so the DB + notify path is isolated from the ticker, and so
// the firing decision (shouldFireIdlePush) can be unit-tested without a
// real runner.
func (s *Server) idleWatcherTick(state *idleWatcherState) {
	running, idle := s.runner.RunningAndIdle()
	seen := make(map[string]bool, len(running))
	idleSet := make(map[string]bool, len(idle))
	for _, id := range idle {
		idleSet[id] = true
	}

	now := time.Now()
	for _, id := range running {
		seen[id] = true
		// Idle bit comes from the RunningAndIdle snapshot above so the
		// (running, idle) view stays internally consistent within this tick.
		// LastInput needs a fresh runner.Get because it isn't in the snapshot
		// — that's a small TOCTOU window: the session can exit between the
		// snapshot and Get. Get returns nil on exit, lastInput stays the
		// zero time, and the session-exited cleanup below prunes the
		// watcher state on the next tick. Net effect: zero lastInput on a
		// dying session is suppressed by shouldFireIdlePush — either by the
		// no-input gate (never-fired sessions) or by the re-arm condition
		// (previously-fired sessions, where zero is never strictly after a
		// recorded pushedAt). Worst case: one suppressed push for a session
		// about to vanish.
		var lastInput time.Time
		if sess := s.runner.Get(id); sess != nil {
			lastInput = sess.LastInput()
		}
		// Emit session.idle on every busy→idle transition (plugin
		// substrate, PR 2). Independent of push gating — plugins want
		// fine-grained visibility, push wants throttling. The state
		// mutation happens inside shouldFireIdlePush below, so the
		// transition check must read state BEFORE that call.
		if idleTransitioned(state, id, idleSet[id]) {
			events.Emit(model.EventTypeSessionIdle, id, nil)
		}
		if !shouldFireIdlePush(state, id, idleSet[id], lastInput, now) {
			continue
		}
		// Push path is gated independently — every other emission path in
		// this loop is plugin-visible, but push.Notify needs an actual
		// push manager.
		if s.push == nil {
			continue
		}
		task, err := s.db.Get(id)
		if err != nil || task == nil {
			continue
		}
		name := task.Name
		if name == "" {
			name = id
		}
		body := "Agent idle — needs attention"
		if task.Status == model.StatusInReview {
			body = "Ready for review"
		}
		uxlog.Log("[push] idle transition task=%s name=%q", id, name)
		// Empty throttle key: shouldFireIdlePush is the sole gate. See
		// context/knowledge/gotchas/web-remote.md (Web Push / VAPID) for
		// why the old "idle:<id>" 5-min throttle was removed.
		s.push.Notify("", name, body, id)
	}

	s.detectNeedsInputTick(state, running, idle, func(id string) []byte {
		if sess := s.runner.Get(id); sess != nil {
			return sess.RecentOutputTail(needsInputScanBytes)
		}
		return nil
	})

	// Drop entries for sessions that exited.
	for id := range state.idleNow {
		if !seen[id] {
			delete(state.idleNow, id)
			delete(state.seenBefore, id)
			delete(state.pushedAt, id)
		}
	}
}

// detectNeedsInputTick scans the idle sessions for the "blocked waiting on the
// user" signature, publishes the resulting set onto the runner (so /api/tasks
// reflects it with no TUI attached), and emits session.needs_input events on
// every enter/leave transition — parallel to the session.idle emission above.
// One event type carries both edges; the payload's needs_input bool
// distinguishes enter (true) from clear (false).
//
// tailOf supplies each task's recent PTY output; idleWatcherTick reads from
// the live session ring buffer via the runner (the daemon is the sole PTY
// reader, so the ring is always populated — correct without any TUI). The
// state's needsInputNow map is replaced wholesale each tick, so exited
// sessions (absent from running/idle) drop out and fire a clear event.
func (s *Server) detectNeedsInputTick(state *idleWatcherState, running, idle []string, tailOf func(string) []byte) {
	prev := make([]string, 0, len(state.needsInputNow))
	for id := range state.needsInputNow {
		prev = append(prev, id)
	}
	needs := computeNeedsInput(idle, running, prev, tailOf)
	needsSet := make(map[string]bool, len(needs))
	for _, id := range needs {
		needsSet[id] = true
	}
	for _, id := range needs {
		if !state.needsInputNow[id] {
			uxlog.Log("[needsinput] task=%s entered needs-input", id)
			events.Emit(model.EventTypeSessionNeedsInput, id, map[string]bool{"needs_input": true})
		}
	}
	for id := range state.needsInputNow {
		if !needsSet[id] {
			uxlog.Log("[needsinput] task=%s left needs-input", id)
			events.Emit(model.EventTypeSessionNeedsInput, id, map[string]bool{"needs_input": false})
		}
	}
	state.needsInputNow = needsSet
	s.runner.SetNeedsInputIDs(needs)
}
