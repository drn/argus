package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/uxlog"
)

// reDeliveryID is the allowed character set for delivery IDs: alphanumeric + hyphen + underscore.
var reDeliveryID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type notifyReq struct {
	Text       string `json:"text"`
	Submit     *bool  `json:"submit"` // required, must be true
	DeliveryID string `json:"delivery_id"`
	DeadlineMS int64  `json:"deadline_ms"`
}

type notifyResp struct {
	DeliveryID string `json:"delivery_id"`
	State      string `json:"state"`
}

type cancelNotifyResp struct {
	DeliveryID string `json:"delivery_id"`
	Cancelled  bool   `json:"cancelled"`
}

// handleNotify handles POST /api/tasks/{id}/notify.
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reliable delivery not available"})
		return
	}

	taskID := r.PathValue("id")
	task, err := s.db.Get(taskID)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req notifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	if req.Submit == nil || !*req.Submit {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "submit must be true"})
		return
	}
	if req.DeliveryID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delivery_id is required"})
		return
	}
	if len(req.DeliveryID) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delivery_id too long (max 128 bytes)"})
		return
	}
	if !reDeliveryID.MatchString(req.DeliveryID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delivery_id must contain only alphanumeric characters, hyphens, or underscores"})
		return
	}
	// deadline_ms=0 means "use the default (5 minutes)". Any explicit value
	// must be between 1000ms (1 second) and 3600000ms (1 hour).
	if req.DeadlineMS != 0 && req.DeadlineMS < 1000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deadline_ms minimum is 1000 (1 second); use 0 for the default (5 minutes)"})
		return
	}
	if req.DeadlineMS > 3_600_000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deadline_ms exceeds maximum of 3600000 (1 hour)"})
		return
	}

	// Idempotent: already submitted → 200.
	if existing := s.notifier.DeliveryState(taskID, req.DeliveryID); existing == notify.StateSubmitted {
		writeJSON(w, http.StatusOK, notifyResp{DeliveryID: req.DeliveryID, State: string(notify.StateSubmitted)})
		return
	}

	_ = s.notifier.ReliableNotify(taskID, req.Text, req.DeliveryID, notify.NotifyOpts{
		DeadlineMS: req.DeadlineMS,
	})
	// caller cancels via DELETE /notify/{delivery_id}; the cancel func is not stored here.

	// Attempt an inline Reconcile so that if the session is already idle and
	// unfocused the delivery submits immediately and the response says "submitted".
	// If the session is busy or focused, the delivery stays pending for the next
	// idle-watcher tick.
	s.notifier.Reconcile(time.Now())

	// DeliveryState returns StateSubmitted when the delivery was genuinely
	// submitted, StatePending when it is still queued, or "" when it was
	// removed without submission (deadline expiry, write failure, cancel).
	// Never assume "" means submitted — that would misreport failed removals.
	// Map "" to pending to keep the API response in the documented set.
	state := s.notifier.DeliveryState(taskID, req.DeliveryID)
	if state == "" {
		state = notify.StatePending
	}
	uxlog.Log("[api] notify registered task=%s delivery_id=%s state=%s", taskID, req.DeliveryID, state)
	writeJSON(w, http.StatusAccepted, notifyResp{DeliveryID: req.DeliveryID, State: string(state)})
}

// handleCancelNotify handles DELETE /api/tasks/{id}/notify/{delivery_id}.
func (s *Server) handleCancelNotify(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reliable delivery not available"})
		return
	}

	taskID := r.PathValue("id")
	deliveryID := r.PathValue("delivery_id")

	// Check existence before cancel to determine response.
	prior := s.notifier.DeliveryState(taskID, deliveryID)
	wasPending := prior == notify.StatePending

	s.notifier.Cancel(taskID, deliveryID)

	uxlog.Log("[api] notify cancel task=%s delivery_id=%s was_pending=%v", taskID, deliveryID, wasPending)
	writeJSON(w, http.StatusOK, cancelNotifyResp{DeliveryID: deliveryID, Cancelled: wasPending})
}
