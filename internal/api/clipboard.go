package api

import (
	"encoding/json"
	"net/http"

	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/uxlog"
)

// SetClipboard wires the agent-staged clipboard store into the API server
// so the /api/tasks/{id}/clipboard endpoints and the SSE stream can read,
// write, clear, and subscribe. Called by the daemon during startup.
func (s *Server) SetClipboard(store *clipboard.Store) {
	s.clipboard = store
}

// clipboardSetReq is the wire shape for POST /api/tasks/{id}/clipboard.
type clipboardSetReq struct {
	Text string `json:"text"`
}

// clipboardGetResp is the wire shape returned by GET /api/tasks/{id}/clipboard.
type clipboardGetResp struct {
	Text string `json:"text"`
}

// handleClipboardGet returns the staged text for a task. Returns 204 when
// no payload is staged so the PWA can render an empty state without parsing
// JSON. The {id} path parameter is the task ID. Unknown task IDs return 404
// to match the rest of the task-scoped API surface.
func (s *Server) handleClipboardGet(w http.ResponseWriter, r *http.Request) {
	if s.clipboard == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.Get(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	text, ok := s.clipboard.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, clipboardGetResp{Text: text})
}

// handleClipboardSet stages text for a task. Primarily used by automated
// tests and the PWA harness; production agents go through the MCP tool
// instead. Reuses the daemon's clipboard.Store so size limits and TTL apply.
func (s *Server) handleClipboardSet(w http.ResponseWriter, r *http.Request) {
	if s.clipboard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "clipboard unavailable"})
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.Get(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	// Cap body before json.Decode to prevent a streamed multi-MiB payload
	// from being buffered into memory before the store-side size check
	// kicks in. 1 MiB matches the store cap; the +4 KiB headroom covers
	// the JSON envelope (`{"text":"…"}` plus any harmless whitespace).
	r.Body = http.MaxBytesReader(w, r.Body, clipboard.MaxTextSize+4096)
	var req clipboardSetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := s.clipboard.Set(id, req.Text); err != nil {
		uxlog.Log("[clipboard] set rejected: task=%s err=%v", id, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[clipboard] set: task=%s bytes=%d", id, len(req.Text))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleClipboardClear removes any staged text for a task. The PWA calls
// this immediately after a successful navigator.clipboard.writeText so the
// Copy button hides everywhere.
func (s *Server) handleClipboardClear(w http.ResponseWriter, r *http.Request) {
	if s.clipboard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "clipboard unavailable"})
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.Get(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	s.clipboard.Clear(id)
	uxlog.Log("[clipboard] clear: task=%s", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
