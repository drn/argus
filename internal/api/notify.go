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
		writeErr(w, http.StatusServiceUnavailable, "reliable delivery not available", nil)
		return
	}

	taskID := r.PathValue("id")
	task, err := s.db.Get(taskID)
	if err != nil || task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req notifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}

	if req.Text == "" {
		writeErr(w, http.StatusBadRequest, "text is required", nil)
		return
	}
	if req.Submit == nil || !*req.Submit {
		writeErr(w, http.StatusBadRequest, "submit must be true", nil)
		return
	}
	if req.DeliveryID == "" {
		writeErr(w, http.StatusBadRequest, "delivery_id is required", nil)
		return
	}
	if len(req.DeliveryID) > 128 {
		writeErr(w, http.StatusBadRequest, "delivery_id too long (max 128 bytes)", nil)
		return
	}
	if !reDeliveryID.MatchString(req.DeliveryID) {
		writeErr(w, http.StatusBadRequest, "delivery_id must contain only alphanumeric characters, hyphens, or underscores", nil)
		return
	}
	// deadline_ms=0 means "use the default (5 minutes)". Any explicit value
	// must be between 1000ms (1 second) and 3600000ms (1 hour).
	if req.DeadlineMS != 0 && req.DeadlineMS < 1000 {
		writeErr(w, http.StatusBadRequest, "deadline_ms minimum is 1000 (1 second); use 0 for the default (5 minutes)", nil)
		return
	}
	if req.DeadlineMS > 3_600_000 {
		writeErr(w, http.StatusBadRequest, "deadline_ms exceeds maximum of 3600000 (1 hour)", nil)
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
		writeErr(w, http.StatusServiceUnavailable, "reliable delivery not available", nil)
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
