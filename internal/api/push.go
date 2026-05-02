package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/db"
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
	idleNow    map[string]bool      // taskID -> last seen idle?
	seenBefore map[string]bool      // taskID -> have we observed this session on a prior tick?
	pushedAt   map[string]time.Time // taskID -> wall-clock time we last fired an idle push
}

func newIdleWatcherState() *idleWatcherState {
	return &idleWatcherState{
		idleNow:    make(map[string]bool),
		seenBefore: make(map[string]bool),
		pushedAt:   make(map[string]time.Time),
	}
}

// idleWatcher periodically polls all running sessions and fires a push when a
// session transitions from non-idle to idle. Coarse but cheap (5s tick).
// Exits when s.stopCh is closed (Server.Shutdown).
//
// Single-goroutine: state is only touched here so no mutex is needed.
func (s *Server) idleWatcher() {
	if s.push == nil {
		return
	}
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

// shouldFireIdlePush applies one observation to the per-task state and
// returns whether the watcher should fire a push for an idle transition.
// Deterministic and I/O-free (the only side effect is mutating the passed
// state) so the firing logic can be unit-tested without wiring up a real
// runner + session + db. It encodes three invariants:
//
//   - First observation of a session never fires: prevents spurious push
//     when an already-idle session enters the watcher's view (e.g. fresh
//     idleWatcher start with running sessions present).
//   - Only busy→idle transitions fire (idle→idle and busy→busy are silent).
//   - One push per work cycle: after a push, no further pushes fire for the
//     same task until input arrives via WriteInput. lastInputAt is the
//     session's input timestamp; a push is suppressed if a previous push
//     already covered this idle period (no input since). This is the
//     primary defence against repeat pushes for a stale long-idle agent
//     whose incidental output (heartbeats, cursor redraws) keeps flipping
//     IsIdle false→true.
func shouldFireIdlePush(state *idleWatcherState, id string, isIdle bool, lastInputAt time.Time, now time.Time) bool {
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
		// dying session can't satisfy the re-arm condition (it's never
		// strictly after a recorded pushedAt), so the worst case is one
		// suppressed push for a session about to vanish.
		var lastInput time.Time
		if sess := s.runner.Get(id); sess != nil {
			lastInput = sess.LastInput()
		}
		if !shouldFireIdlePush(state, id, idleSet[id], lastInput, now) {
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

	// Drop entries for sessions that exited.
	for id := range state.idleNow {
		if !seen[id] {
			delete(state.idleNow, id)
			delete(state.seenBefore, id)
			delete(state.pushedAt, id)
		}
	}
}
