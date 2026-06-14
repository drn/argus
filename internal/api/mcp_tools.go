package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/uxlog"
)

// SetMCPRegistry wires the runtime plugin-tool registry. Called by the daemon
// after constructing the Registry so both the MCP server and the API expose a
// consistent view. Must be called before ListenAndServe — Set* fields are
// read at request time without a mutex.
func (s *Server) SetMCPRegistry(reg *mcp.Registry) {
	s.mcpRegistry = reg
}

// registerMCPToolReq mirrors the JSON body POST /api/mcp/tools accepts. Kept
// in lower-case JSON tags so the plugin's HTTP body matches the contract
// reference in the plan verbatim.
type registerMCPToolReq struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	CallbackURL string          `json:"callback_url"`
	AuthHeader  string          `json:"auth_header"`
}

// maxRegisterMCPToolBytes caps the inbound JSON body. Generous enough for a
// reasonable input_schema (the registry caps the schema itself separately).
const maxRegisterMCPToolBytes = 256 * 1024

// handleRegisterMCPTool wires the plugin-side POST /api/mcp/tools endpoint
// from the plan. Plugin token only — the namespace enforcement gates on the
// caller's scope, so a master credential has no scope to enforce against.
func (s *Server) handleRegisterMCPTool(w http.ResponseWriter, r *http.Request) {
	if s.mcpRegistry == nil {
		writeErr(w, http.StatusServiceUnavailable, "mcp registry not configured", nil)
		return
	}
	scope := pluginScopeFromAuth(r)
	if scope == "" {
		writeErr(w, http.StatusForbidden, "plugin-scoped token required", nil)
		return
	}
	var body registerMCPToolReq
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterMCPToolBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	err := s.mcpRegistry.Register(scope, mcp.ToolRegistration{
		Name:        body.Name,
		Description: body.Description,
		InputSchema: body.InputSchema,
		CallbackURL: body.CallbackURL,
		AuthHeader:  body.AuthHeader,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}
	uxlog.Log("[plugin] mcp tool registered: scope=%s name=%s", scope, body.Name)
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name, "scope": scope})
}

// handleUnregisterMCPTool wires DELETE /api/mcp/tools/{name}. The plugin can
// drop its own tools; the master credential can drop any tool (operator
// cleanup). A device token cannot — device tokens have no plugin namespace.
func (s *Server) handleUnregisterMCPTool(w http.ResponseWriter, r *http.Request) {
	if s.mcpRegistry == nil {
		writeErr(w, http.StatusServiceUnavailable, "mcp registry not configured", nil)
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	authTag := r.Header.Get("X-Argus-Auth")
	scope := pluginScopeFromAuth(r)
	switch {
	case authTag == "master":
		// Master removes anything — pass empty scope, Registry interprets
		// that as the cleanup-credential.
		if err := s.mcpRegistry.Unregister("", name); err != nil {
			writeErr(w, http.StatusBadRequest, "", err)
			return
		}
	case scope != "":
		if err := s.mcpRegistry.Unregister(scope, name); err != nil {
			// Registry's "not owned by this scope" error is a plain
			// errors.New — string-match the substring so a 403 (vs 400)
			// is returned for the cross-scope rejection.
			if strings.Contains(err.Error(), "not owned") {
				writeErr(w, http.StatusForbidden, "", err)
				return
			}
			writeErr(w, http.StatusBadRequest, "", err)
			return
		}
	default:
		writeErr(w, http.StatusForbidden, "plugin-scoped or master token required", nil)
		return
	}
	uxlog.Log("[plugin] mcp tool unregistered: auth=%s scope=%s name=%s", authTag, scope, name)
	writeJSON(w, http.StatusOK, map[string]string{"unregistered": name})
}

// pluginScopeFromAuth extracts the plugin scope from the X-Argus-Auth header
// set by authMiddleware. Returns "" when the caller is master or device.
func pluginScopeFromAuth(r *http.Request) string {
	tag := r.Header.Get("X-Argus-Auth")
	if !strings.HasPrefix(tag, "scope:") {
		return ""
	}
	return strings.TrimPrefix(tag, "scope:")
}
