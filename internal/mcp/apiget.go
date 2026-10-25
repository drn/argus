package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// APIGetResult is the outcome of one in-process REST GET.
type APIGetResult struct {
	Status      int
	ContentType string
	Body        []byte
	Truncated   bool
}

// APIGetter performs a read-only GET against the daemon's REST API. The
// daemon injects (*api.Server).ReadOnlyGet; internal/mcp cannot import
// internal/api (api imports mcp for the plugin registry). A returned error
// means the request was refused before routing (validation, denied route); a
// routed request returns a result even for non-2xx statuses.
type APIGetter func(ctx context.Context, path string) (APIGetResult, error)

// apiGetToolDefs are exposed only when SetAPIGetter has been called.
var apiGetToolDefs = []Tool{
	{
		Name: "argus_api_get",
		Description: `Read Argus daemon state over its REST API without shelling out (no curl, no token, no permission prompt). GET only — it cannot change anything. Returns the response body (JSON for most routes).

` + "`path`" + ` is an API path such as "/api/tasks" ("/api" is optional: "tasks" works too) and may carry a query string. No "..", scheme, or host. Unknown routes return an HTTP 404 error. Streaming routes (SSE) and master-token-only routes are refused. Output over 1 MiB is truncated.

Useful routes:
- /api/tasks — all tasks; /api/tasks/{id} — one task (also /output, /links, /meta, /inbox, /git/status, /git/diff, /files, /artifacts, /claude-sessions)
- /api/tasks/{id}/bridge — bridge state for a task; /api/tasks/{id}/bridge/ui — its bridge UI state; /api/bridge/shadow — bridge shadow data (present when the bridge feature is installed)
- /api/hera — hera orchestrator/role roster
- /api/status, /api/projects, /api/backends, /api/accounts, /api/schedules, /api/config, /api/sessions/state
- /api/tasks-raw, /api/tasks/{id}/raw — full model.Task shapes`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "API path to GET, e.g. \"/api/tasks\" or \"/api/tasks/<id>/bridge\". Optional query string allowed."},
			},
			"required": []string{"path"},
		},
	},
}

// SetAPIGetter wires the argus_api_get tool. Safe to call after the MCP
// listener is already serving (the daemon starts MCP before the REST API).
func (s *Server) SetAPIGetter(g APIGetter) {
	s.apiGetMu.Lock()
	s.apiGet = g
	s.apiGetMu.Unlock()
}

func (s *Server) apiGetter() APIGetter {
	s.apiGetMu.RLock()
	defer s.apiGetMu.RUnlock()
	return s.apiGet
}

func (s *Server) apiGetEnabled() bool { return s.apiGetter() != nil }

func (s *Server) toolAPIGet(id interface{}, args json.RawMessage) *Response {
	get := s.apiGetter()
	if get == nil {
		return toolError(id, "argus_api_get is not available: the daemon REST API is not enabled")
	}
	var p struct {
		Path string `json:"path"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	if strings.TrimSpace(p.Path) == "" {
		return toolError(id, "path is required")
	}

	res, err := get(s.shutdownCtx, p.Path)
	if err != nil {
		return toolError(id, err.Error())
	}
	body := string(res.Body)
	if res.Truncated {
		body += "\n[truncated: response exceeded 1 MiB]"
	}
	if res.Status < 200 || res.Status > 299 {
		return toolError(id, fmt.Sprintf("HTTP %d: %s", res.Status, strings.TrimSpace(body)))
	}
	return toolResult(id, body)
}
