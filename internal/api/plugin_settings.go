package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/settings"
	"github.com/drn/argus/internal/uxlog"
)

// pluginSectionMaxBodyBytes caps a single POST /api/plugins/settings/sections
// request body. 256 KiB comfortably fits a form with ~250 enum-rich fields —
// well past anything plausible — while keeping a runaway client from pinning
// the daemon in JSON decoding. Same intent as taskMetaMaxBodyBytes.
const pluginSectionMaxBodyBytes = 256 * 1024

// authScope extracts the plugin scope from the auth tag header. The auth
// middleware tags requests with `X-Argus-Auth: scope:<name>` when a
// plugin-scoped token is presented; this returns just `<name>`. Returns
// the empty string for non-scoped tokens (master / device) so callers can
// gate on a non-empty result.
func authScope(r *http.Request) string {
	tag := r.Header.Get("X-Argus-Auth")
	if !strings.HasPrefix(tag, "scope:") {
		return ""
	}
	return strings.TrimPrefix(tag, "scope:")
}

// handleRegisterPluginSection accepts a section registration from a
// plugin-scoped token. The scope is taken from the auth header (NOT from the
// body) so plugins cannot register into another plugin's namespace.
//
// Body shape mirrors the plan's example:
//
//	{
//	  "title": "...",
//	  "type": "form",
//	  "callback_url": "...",
//	  "spec": { "fields": [...] }
//	}
//
// Inline `fields` (no `spec` envelope) is also accepted — see
// settings.ParseSection.
//
// On success returns the parsed section's identity and 201. Plugins
// re-registering the same title get 200 with the same body shape.
func (s *Server) handleRegisterPluginSection(w http.ResponseWriter, r *http.Request) {
	scope := authScope(r)
	if scope == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "plugin-scoped token required"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pluginSectionMaxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}

	sec, err := settings.ParseSection(scope, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	pluginRow, err := db.FromSection(sec)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	existed := false
	if list, lerr := s.db.ListPluginSections(); lerr == nil {
		for _, row := range list {
			if row.Scope == sec.Scope && row.Title == sec.Title {
				existed = true
				break
			}
		}
	}

	id, err := s.db.UpsertPluginSection(pluginRow)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.pluginSections != nil {
		// Best-effort: update the in-memory mirror so live readers see the
		// change without re-querying the DB. The DB is the source of truth;
		// a registry miss is silently rebuilt on the next boot or List.
		_ = s.pluginSections.Register(sec) //nolint:errcheck // validated above
	}
	uxlog.Log("[api] plugin section register scope=%q title=%q id=%d existed=%v", sec.Scope, sec.Title, id, existed)

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"scope": sec.Scope,
		"title": sec.Title,
		"id":    id,
	})
}

// handleUnregisterPluginSection removes a section. Path is
// /api/plugins/settings/sections/{scope}/{title}. A plugin-scoped token
// may only remove sections it owns; master tokens can remove any section
// (operator escape hatch — see also DeletePluginSectionsByScope which
// fires from the token revocation path).
func (s *Server) handleUnregisterPluginSection(w http.ResponseWriter, r *http.Request) {
	pathScope := r.PathValue("scope")
	pathTitle := r.PathValue("title")
	if pathScope == "" || pathTitle == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope and title path segments are required"})
		return
	}

	caller := authScope(r)
	master := r.Header.Get("X-Argus-Auth") == "master"
	if !master && caller != pathScope {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot unregister another plugin's section"})
		return
	}

	removed, err := s.db.DeletePluginSection(pathScope, pathTitle)
	if err != nil {
		if errors.Is(err, db.ErrPluginSectionInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "section not found"})
		return
	}
	if s.pluginSections != nil {
		s.pluginSections.Unregister(pathScope, pathTitle)
	}
	uxlog.Log("[api] plugin section unregister scope=%q title=%q", pathScope, pathTitle)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// pluginSectionJSON is the wire shape for GET responses. We re-expose the
// parsed spec instead of the raw JSON string so clients (the TUI's
// apistore) don't have to double-parse.
type pluginSectionJSON struct {
	Scope       string               `json:"scope"`
	Title       string               `json:"title"`
	Type        string               `json:"type"`
	CallbackURL string               `json:"callback_url"`
	Fields      []settings.FormField `json:"fields"`
}

// handleListPluginSections returns every registered section. Open to any
// authenticated request (master / device / plugin scope) — the TUI's
// apistore polls this on every config refresh, and pluginsthemselves
// occasionally inspect to verify their own registration succeeded.
func (s *Server) handleListPluginSections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.ListPluginSections()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]pluginSectionJSON, 0, len(rows))
	for _, row := range rows {
		sec, err := row.ToSection()
		if err != nil {
			uxlog.Log("[api] plugin section corrupt scope=%q title=%q: %v", row.Scope, row.Title, err)
			continue
		}
		var fields []settings.FormField
		if sec.Spec != nil {
			fields = sec.Spec.Fields
		}
		out = append(out, pluginSectionJSON{
			Scope:       sec.Scope,
			Title:       sec.Title,
			Type:        string(sec.Type),
			CallbackURL: sec.CallbackURL,
			Fields:      fields,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sections": out})
}

// handleSubmitPluginSectionValues is the save-side of the form contract: the
// TUI POSTs the user-entered `{key: value, ...}` map here, and the daemon
// proxies the body to the plugin's `callback_url`. Centralizing the proxy
// in the daemon keeps the TUI free of egress concerns (which matter most
// in `--remote` mode where the TUI is on a phone with no LAN route to the
// plugin) and gives audit log a single chokepoint.
//
// Path: /api/plugins/settings/sections/{scope}/{title}/submit. Open to any
// authenticated request — the user-initiated save is by definition driven
// from an authenticated TUI session.
func (s *Server) handleSubmitPluginSectionValues(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	title := r.PathValue("title")
	if scope == "" || title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope and title path segments are required"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pluginSectionMaxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	// Validate JSON well-formedness up front so a malformed body doesn't get
	// forwarded to the plugin (which would have to reject it itself).
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be a JSON object: " + err.Error()})
		return
	}

	rows, err := s.db.ListPluginSections()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var (
		callbackURL string
		authHeader  string
		found       bool
	)
	for _, row := range rows {
		if row.Scope == scope && row.Title == title {
			callbackURL = row.CallbackURL
			authHeader = row.AuthHeader
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "section not found"})
		return
	}

	uxlog.Log("[api] plugin section submit scope=%q title=%q has_auth=%v", scope, title, authHeader != "")
	submit := s.pluginSubmitFn
	if submit == nil {
		submit = defaultPluginSubmit
	}
	statusCode, respBody, perr := submit(r.Context(), callbackURL, authHeader, body)
	if perr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": perr.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(respBody)
}

// defaultPluginSubmit POSTs body to callbackURL with a 10s timeout. Used by
// the form-submit proxy when no test seam has overridden s.pluginSubmitFn.
// Returns the upstream status and body verbatim so the TUI can surface
// failures to the user with the plugin's own error message. authHeader, if
// non-empty, is set verbatim as the Authorization header — plugins gating
// their callback endpoint (hera's MCP listener requires this on /mcp/*)
// register the expected value at section-register time.
func defaultPluginSubmit(ctx context.Context, callbackURL, authHeader string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, pluginSectionMaxBodyBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}
