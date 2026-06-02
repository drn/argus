package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/drn/argus/internal/uxlog"
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
	// Master-only — listing all tokens reveals the per-device roster
	// (labels, last4, last_used). A compromised device token shouldn't be
	// able to fingerprint other devices.
	if requireMaster(w, r) {
		return
	}
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
	if requireMaster(w, r) {
		return
	}
	var req createTokenReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	// Empty body is allowed (label defaults to "device"); reject other decode errors.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
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
	if requireMaster(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	// Snapshot the scope before revoke so the plugin-MCP cascade can target
	// the right rows. FindAPITokenByHash hides revoked tokens, so doing this
	// after the revoke would lose the scope. Best-effort: if the lookup
	// fails (DB transient), the revoke still proceeds and the registry sweep
	// is skipped — the idle sweeper will catch the orphaned tools within
	// DefaultIdleWindow.
	tok, _ := s.db.FindAPITokenByID(id)
	if err := s.db.RevokeAPIToken(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if tok != nil && tok.Scope != "" && s.mcpRegistry != nil {
		n, err := s.mcpRegistry.UnregisterScope(tok.Scope)
		if err != nil {
			uxlog.Log("[plugin] revoke cascade failed: scope=%s err=%v", tok.Scope, err)
		} else if n > 0 {
			uxlog.Log("[plugin] revoke cascade: scope=%s removed=%d", tok.Scope, n)
		}
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": id})
}
