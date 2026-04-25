package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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
	s.push.Notify("", "Argus test", "Push notifications are working", "")
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// idleWatcher periodically polls all running sessions and fires a push when a
// session transitions from non-idle to idle. Coarse but cheap (5s tick).
// Exits when s.stopCh is closed (Server.Shutdown).
//
// Single-goroutine: idleNow is only touched here so no mutex is needed.
func (s *Server) idleWatcher() {
	if s.push == nil {
		return
	}
	idleNow := make(map[string]bool) // taskID -> last seen idle?
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-tick.C:
		}

		running, _ := s.runner.RunningAndIdle()
		seen := make(map[string]bool, len(running))

		for _, id := range running {
			seen[id] = true
			sess := s.runner.Get(id)
			if sess == nil {
				continue
			}
			isIdle := sess.IsIdle()
			wasIdle := idleNow[id]
			idleNow[id] = isIdle
			if isIdle && !wasIdle {
				// Idle transition — fire push.
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
				s.push.Notify("idle:"+id, name, body, id)
			}
		}

		// Drop entries for sessions that exited; also tell the push manager
		// to forget throttle entries for them so the lastSent map doesn't grow.
		for id := range idleNow {
			if !seen[id] {
				delete(idleNow, id)
				s.push.ForgetTask(id)
			}
		}
	}
}
