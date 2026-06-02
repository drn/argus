package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/drn/argus/internal/tui/views"
)

// pluginViewJSON is the wire shape for /api/plugins/views responses and the
// POST request body. created_at is RFC3339 nano per the rest of the API.
type pluginViewJSON struct {
	ID          int64  `json:"id"`
	Scope       string `json:"scope"`
	Title       string `json:"title"`
	Hotkey      string `json:"hotkey,omitempty"`
	CallbackURL string `json:"callback_url"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// pluginViewCreateReq is the POST body. The row's scope is derived from the
// auth header (master → "", scope:<name> → "<name>") rather than the body,
// so a plugin cannot register into another plugin's namespace.
type pluginViewCreateReq struct {
	Title       string `json:"title"`
	Hotkey      string `json:"hotkey"`
	CallbackURL string `json:"callback_url"`
}

func toPluginViewJSON(v *views.View) pluginViewJSON {
	return pluginViewJSON{
		ID:          v.ID,
		Scope:       v.Scope,
		Title:       v.Title,
		Hotkey:      v.Hotkey,
		CallbackURL: v.CallbackURL,
		CreatedAt:   v.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

// handleCreatePluginView registers a new plugin-owned top-level view.
//
// Master tokens register into the empty scope (""); scope:<name> tokens
// register under their own scope. Device tokens are rejected. The row's
// scope is forced from the auth header — the body has no scope field.
func (s *Server) handleCreatePluginView(w http.ResponseWriter, r *http.Request) {
	scope, hasScope := scopeFromAuth(r)
	isMaster := r.Header.Get("X-Argus-Auth") == "master"
	if !isMaster && !hasScope {
		http.Error(w, `{"error":"master or scope token required"}`, http.StatusForbidden)
		return
	}
	var req pluginViewCreateReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	reg := views.New(s.db)
	v, err := reg.Register(scope, req.Title, req.Hotkey, req.CallbackURL)
	if err != nil {
		switch {
		case errors.Is(err, views.ErrTitleRequired),
			errors.Is(err, views.ErrCallbackURLRequired):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, views.ErrViewExists):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusCreated, toPluginViewJSON(v))
}

// handleListPluginViews returns registered views ordered by insertion.
// Master tokens see every row; scope:<name> tokens see only rows whose
// scope matches their auth tag. Device tokens are rejected.
func (s *Server) handleListPluginViews(w http.ResponseWriter, r *http.Request) {
	scope, hasScope := scopeFromAuth(r)
	isMaster := r.Header.Get("X-Argus-Auth") == "master"
	if !isMaster && !hasScope {
		http.Error(w, `{"error":"master or scope token required"}`, http.StatusForbidden)
		return
	}
	reg := views.New(s.db)
	all := reg.List()
	out := make([]pluginViewJSON, 0, len(all))
	for _, v := range all {
		if hasScope && v.Scope != scope {
			continue
		}
		out = append(out, toPluginViewJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": out})
}

// handleDeletePluginView removes the view by ID.
//
// The spec sketch called for DELETE /api/plugins/views/{scope}/{title}, but
// Go's net/http.ServeMux canonicalises `//` segments and 307-redirects, which
// makes the spec-shape unusable when scope is "". Using {id} matches the
// existing /api/tokens/{id} precedent.
//
// Master tokens can delete any row. scope:<name> tokens can only delete rows
// whose scope matches theirs — a cross-scope delete returns 403, never 404,
// so a plugin cannot probe for the existence of another plugin's views.
func (s *Server) handleDeletePluginView(w http.ResponseWriter, r *http.Request) {
	scope, hasScope := scopeFromAuth(r)
	isMaster := r.Header.Get("X-Argus-Auth") == "master"
	if !isMaster && !hasScope {
		http.Error(w, `{"error":"master or scope token required"}`, http.StatusForbidden)
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	all, err := s.db.PluginViews()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var match *pluginViewJSON
	for _, row := range all {
		if row.ID == id {
			j := toPluginViewJSON(&views.View{
				ID: row.ID, Scope: row.Scope, Title: row.Title,
				Hotkey: row.Hotkey, CallbackURL: row.CallbackURL, CreatedAt: row.CreatedAt,
			})
			match = &j
			break
		}
	}
	if match == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "view not found"})
		return
	}
	if hasScope && match.Scope != scope {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "view belongs to a different scope"})
		return
	}
	reg := views.New(s.db)
	if err := reg.Unregister(match.Scope, match.Title); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": match.ID})
}
