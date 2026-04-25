package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type tokenView struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Last4     string `json:"last4"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  int64  `json:"last_used,omitempty"`
	Revoked   bool   `json:"revoked,omitempty"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.APITokens()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]tokenView, 0, len(tokens))
	for _, t := range tokens {
		var lastUsed int64
		if !t.LastUsed.IsZero() {
			lastUsed = t.LastUsed.Unix()
		}
		out = append(out, tokenView{
			ID:        t.ID,
			Label:     t.Label,
			Last4:     t.Last4,
			CreatedAt: t.CreatedAt.Unix(),
			LastUsed:  lastUsed,
			Revoked:   t.Revoked,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

type createTokenReq struct {
	Label string `json:"label"`
}

// handleCreateToken mints a new device token. Master-only — device tokens
// can't mint more device tokens.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Argus-Auth") != "master" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "master token required to mint new tokens"})
		return
	}
	var req createTokenReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Label == "" {
		req.Label = "device"
	}
	plain, id, err := MintToken(s.db, req.Label)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":    id,
		"label": req.Label,
		"token": plain, // plaintext — only returned at mint time
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Argus-Auth") != "master" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "master token required"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := s.db.RevokeAPIToken(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": id})
}
