package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// messagesMaxBodyBytes is the HTTP-level cap on a POST /messages body. The
// MCP InsertMessage path enforces the 64 KiB body limit separately on the
// `body` field; this is the outer envelope including JSON framing.
const messagesMaxBodyBytes = model.MaxMessageBodyBytes + 4*1024

// handleListInbox returns the inbox for the path-bound task. Same filter
// surface as the task_inbox MCP tool: ?unread_only=true&sender=<id>&since=<rfc3339>&limit=<n>.
//
// Per-task scope — open to device tokens because the PWA inbox view runs on
// mobile. Reads are safe (no mutation) and tied to the path-bound task ID.
func (s *Server) handleListInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.Get(id); err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	q := r.URL.Query()
	// Default unread_only=true to mirror the MCP tool default; pass
	// ?unread_only=false to include already-acked messages.
	unread := true
	if v := q.Get("unread_only"); v != "" {
		switch v {
		case "false", "0", "no":
			unread = false
		}
	}

	filter := db.InboxFilter{
		UnreadOnly: unread,
		Sender:     q.Get("sender"),
	}
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			// Fall back to second-precision RFC3339 before bailing.
			t, err = time.Parse(time.RFC3339, since)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since (must be RFC3339): " + err.Error()})
				return
			}
		}
		filter.Since = t
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		filter.Limit = n
	}

	msgs, err := s.db.Inbox(id, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if msgs == nil {
		msgs = []*model.TaskMessage{}
	}

	unreadCount, err := s.db.UnreadCount(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":     msgs,
		"unread_count": unreadCount,
	})
}

// handleSendMessage stages a new message from the path-bound task. Body:
//
//	{"to": "<task_id>", "body": "...", "kind": "note|question|answer", "in_reply_to": "<msg_id>"}
//
// requireMaster — sending a message from one task to another mutates shared
// state across tasks. The mobile inbox UI is read-only; outbound sends are
// admin-tier (or come in via MCP from the agent itself).
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	from := r.PathValue("id")
	if _, err := s.db.Get(from); err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, messagesMaxBodyBytes)
	var req struct {
		To        string `json:"to"`
		Body      string `json:"body"`
		Kind      string `json:"kind"`
		InReplyTo string `json:"in_reply_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.To == "" || req.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and body are required"})
		return
	}
	if _, err := s.db.Get(req.To); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recipient task not found: " + req.To})
		return
	}

	kind := model.MessageKind(req.Kind)
	if kind == "" {
		kind = model.KindNote
	}

	msg, err := s.db.InsertMessage(&model.TaskMessage{
		From:      from,
		To:        req.To,
		Kind:      kind,
		Body:      req.Body,
		InReplyTo: req.InReplyTo,
	})
	if err != nil {
		switch {
		case errors.Is(err, db.ErrMessageBodyTooLarge),
			errors.Is(err, db.ErrMessageSelfSend),
			errors.Is(err, db.ErrMessageInboxFull),
			errors.Is(err, db.ErrMessageRateLimited):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         msg.ID,
		"created_at": msg.CreatedAt,
	})
}

// handleAckInbox marks the supplied message IDs read. Body:
//
//	{"ids": ["<msg_id>", ...]}
//
// IDs not belonging to the path-bound task are silently ignored; returned
// count is only the rows actually flipped.
//
// Per-task scope — same tier as inbox read. The PWA inbox view calls this
// after the user taps "Mark read".
func (s *Server) handleAckInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids is required"})
		return
	}

	n, err := s.db.AckMessages(id, req.IDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acked": n})
}
