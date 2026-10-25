package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/uxlog"
)

const (
	// readOnlyTimeout bounds a single in-process GET. Every non-streaming
	// handler finishes well inside it; it exists so a wedged handler can't
	// pin the calling MCP request forever.
	readOnlyTimeout = 15 * time.Second
	// readOnlyMaxBody caps the returned body so a large artifact/diff can't
	// flood an agent's context. Truncation is reported on the result.
	readOnlyMaxBody = 1 << 20
)

// readOnlyDenied are GET patterns ReadOnlyGet refuses even though they are
// GET routes: SSE streams never complete, so an in-process recorder would
// block until the timeout.
var readOnlyDenied = map[string]bool{
	"GET /api/events/stream":     true,
	"GET /api/tasks/{id}/stream": true,
}

// readOnlyMux is the router ReadOnlyGet dispatches through. routes() only
// registers closures over s, so one lazily-built instance is shared.
func (s *Server) readOnlyMux() *http.ServeMux {
	s.roOnce.Do(func() { s.roMux = s.routes() })
	return s.roMux
}

// ReadOnlyGet performs a GET against this server's own REST API in-process,
// through the same router and handlers HTTP clients hit. It exists for the
// argus_api_get MCP tool (openspec add-argus-api-get-tool): agents read daemon
// state without a curl round trip that needs the master token on a command
// line.
//
// Safety properties:
//   - GET only by construction (no method parameter); only patterns registered
//     as "GET /api/..." run, so the mux's "GET /" dashboard catch-all can never
//     answer an unknown path.
//   - The request is stamped as a device-tier caller, so requireMaster
//     handlers refuse it.
//   - No token is read or sent; any master-token substring in the response is
//     redacted anyway.
//
// Validation failures return an error; a routed request always returns a
// result, including non-2xx statuses (the caller decides how to surface them).
func (s *Server) ReadOnlyGet(ctx context.Context, rawPath string) (res mcp.APIGetResult, err error) {
	target, err := normalizeReadOnlyPath(rawPath)
	if err != nil {
		uxlog.Log("[api-get] rejected path %q: %v", rawPath, err)
		return res, err
	}

	ctx, cancel := context.WithTimeout(ctx, readOnlyTimeout)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://argus.local"+target, nil)
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("X-Argus-Auth", "device")

	mux := s.readOnlyMux()
	_, pattern := mux.Handler(req)
	if !strings.HasPrefix(pattern, "GET /api/") {
		uxlog.Log("[api-get] no GET route for %s (pattern %q)", target, pattern)
		return mcp.APIGetResult{Status: http.StatusNotFound, ContentType: "application/json", Body: []byte(`{"error":"no such GET route"}`)}, nil
	}
	if readOnlyDenied[pattern] {
		return res, fmt.Errorf("route %q is a stream and cannot be read through argus_api_get", pattern)
	}

	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("handler panic for %s: %v", pattern, p)
			}
		}()
		mux.ServeHTTP(rec, req)
	}()
	if err != nil {
		uxlog.Log("[api-get] %v", err)
		return res, err
	}

	body := rec.Body.Bytes()
	if s.token != "" {
		body = []byte(strings.ReplaceAll(string(body), s.token, "[redacted]"))
	}
	if len(body) > readOnlyMaxBody {
		body = body[:readOnlyMaxBody]
		res.Truncated = true
	}
	res.Status = rec.Code
	res.ContentType = rec.Header().Get("Content-Type")
	res.Body = body
	uxlog.Log("[api-get] %s -> %d (%d bytes, truncated=%v)", pattern, res.Status, len(body), res.Truncated)
	return res, nil
}

// normalizeReadOnlyPath validates an agent-supplied path and returns the
// request target (path plus optional query) rooted at /api. Accepted forms:
// "tasks", "/tasks", "api/tasks", "/api/tasks?status=active".
func normalizeReadOnlyPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("path contains control characters")
		}
	}
	if strings.ContainsAny(raw, `\#`) {
		return "", errors.New("path must not contain backslashes or fragments")
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return "", errors.New("path must be relative: no scheme or host")
	}
	p, query, _ := strings.Cut(raw, "?")
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return "", fmt.Errorf("invalid percent-encoding: %w", err)
	}
	if strings.Contains(p, "..") || strings.Contains(decoded, "..") {
		return "", errors.New("path must not contain \"..\"")
	}
	if strings.ContainsAny(decoded, "\\\x00") {
		return "", errors.New("path contains an illegal character")
	}
	p = strings.TrimLeft(p, "/")
	if p != "api" && !strings.HasPrefix(p, "api/") {
		p = "api/" + p
	}
	target := "/" + p
	if query != "" {
		target += "?" + query
	}
	return target, nil
}
