package api

import (
	"net/http"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// handleHeraBoard is the parity surface for future web/macOS read-only views.
func (s *Server) handleHeraBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	role, err := s.db.HeraRole(id)
	if err != nil {
		heraErr(w, "board coordinator", err)
		return
	}
	if role.Kind != db.HeraKindCoordinator {
		writeErr(w, http.StatusBadRequest, "board belongs to a coordinator role", nil)
		return
	}
	items, err := s.db.ListHeraBoardItems(id, r.URL.Query().Get("include_resolved") == "true")
	if err != nil {
		uxlog.Log("[board] REST list failed role=%d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "board unavailable", err)
		return
	}
	unread := 0
	for _, i := range items {
		if !i.Resolved && !i.Read {
			unread++
		}
	}
	uxlog.Log("[board] REST list role=%d count=%d unread=%d", id, len(items), unread)
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread_count": unread})
}
