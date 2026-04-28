package api

import (
	"embed"
	"net/http"
	"sort"
	"strings"
)

//go:embed static/index.html static/vendor/* static/manifest.webmanifest static/sw.js static/icon-192.png static/icon-512.png static/apple-touch-icon.png
var staticFS embed.FS

// routes returns the HTTP mux with all API endpoints registered.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Dashboard — served without auth so the page can load and prompt for token.
	mux.HandleFunc("GET /", s.handleDashboard)
	// Web Share Target lands here from iOS/Android shares. Serves the same
	// dashboard HTML; client-side JS reads ?title/&text/&url and prefills the
	// New Task form. Unauthenticated for the same reason as `/`.
	mux.HandleFunc("GET /share", s.handleDashboard)
	// Static assets (xterm.js, css, fit-addon) — also unauthenticated since
	// the dashboard needs them before the user authenticates.
	mux.HandleFunc("GET /vendor/", s.handleVendor)
	// PWA manifest, service worker, and icons — unauthenticated because the
	// browser fetches them at install/registration time before login.
	mux.HandleFunc("GET /manifest.webmanifest", s.handleStatic("manifest.webmanifest", "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", s.handleStatic("sw.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /icon-192.png", s.handleStatic("icon-192.png", "image/png"))
	mux.HandleFunc("GET /icon-512.png", s.handleStatic("icon-512.png", "image/png"))
	mux.HandleFunc("GET /apple-touch-icon.png", s.handleStatic("apple-touch-icon.png", "image/png"))

	// API endpoints — auth is applied by the middleware wrapper in ListenAndServe,
	// but the dashboard route is excluded from auth below.
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("GET /api/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/tasks/{id}/stop", s.handleStopTask)
	mux.HandleFunc("POST /api/tasks/{id}/resume", s.handleResumeTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("GET /api/tasks/{id}/output", s.handleGetOutput)
	mux.HandleFunc("POST /api/tasks/{id}/input", s.handleWriteInput)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleStreamOutput)
	mux.HandleFunc("GET /api/tasks/{id}/size", s.handleGetSize)
	mux.HandleFunc("POST /api/tasks/{id}/resize", s.handleResize)
	mux.HandleFunc("POST /api/tasks/{id}/archive", s.handleArchiveTask)
	mux.HandleFunc("POST /api/tasks/{id}/unarchive", s.handleUnarchiveTask)
	mux.HandleFunc("POST /api/tasks/{id}/rename", s.handleRenameTask)
	mux.HandleFunc("POST /api/tasks/{id}/status", s.handleSetStatus)
	mux.HandleFunc("POST /api/tasks/{id}/fork", s.handleForkTask)
	mux.HandleFunc("POST /api/sessions/stop-all", s.handleStopAll)
	mux.HandleFunc("GET /api/projects/full", s.handleListProjectsFull)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("PUT /api/projects/{name}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{name}", s.handleDeleteProject)
	mux.HandleFunc("GET /api/backends", s.handleListBackends)
	mux.HandleFunc("POST /api/backends", s.handleCreateBackend)
	mux.HandleFunc("PUT /api/backends/{name}", s.handleUpdateBackend)
	mux.HandleFunc("DELETE /api/backends/{name}", s.handleDeleteBackend)
	mux.HandleFunc("GET /api/tasks/{id}/git/status", s.handleGitStatus)
	mux.HandleFunc("GET /api/tasks/{id}/git/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/tasks/{id}/files", s.handleFileTree)
	mux.HandleFunc("GET /api/push/vapid-public-key", s.handleVapidPublicKey)
	mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("DELETE /api/push/subscribe/{id}", s.handlePushUnsubscribe)
	mux.HandleFunc("GET /api/push/subscriptions", s.handlePushList)
	mux.HandleFunc("POST /api/push/test", s.handlePushTest)
	mux.HandleFunc("GET /api/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.handleRevokeToken)
	mux.HandleFunc("GET /api/source-path", s.handleGetSourcePath)
	mux.HandleFunc("PUT /api/source-path", s.handleSetSourcePath)
	mux.HandleFunc("POST /api/update", s.handleUpdateSelf)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/logs/{name}", s.handleGetLog)

	return mux
}

// handleDashboard serves the embedded HTML dashboard.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// handleStatic returns a handler that serves a single embedded static file
// from static/<name> with the given content type.
func (s *Server) handleStatic(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		// Service worker MUST NOT be cached aggressively, otherwise updates won't
		// propagate. Everything else can be cached for a day.
		if name == "sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		w.Write(data) //nolint:errcheck
	}
}

// handleVendor serves embedded static vendor assets (xterm.js, etc).
func (s *Server) handleVendor(w http.ResponseWriter, r *http.Request) {
	// Map /vendor/<file> to static/vendor/<file>; reject anything with ".."
	name := strings.TrimPrefix(r.URL.Path, "/vendor/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/vendor/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	// Content is from a compile-time embed.FS — not user-controlled.
	w.Write(data) //nolint:errcheck,gosec // G705: embedded asset, name validated above
}

// handleListProjects returns the list of configured project names.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.Projects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load projects: " + err.Error()})
		return
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"projects": names})
}
