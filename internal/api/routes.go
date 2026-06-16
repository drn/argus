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
	mux.HandleFunc("POST /api/tasks/{id}/restart", s.handleRestartTask)
	mux.HandleFunc("POST /api/tasks/{id}/resume", s.handleResumeTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("GET /api/tasks/{id}/output", s.handleGetOutput)
	mux.HandleFunc("GET /api/tasks/{id}/links", s.handleGetLinks)
	mux.HandleFunc("POST /api/tasks/{id}/input", s.handleWriteInput)
	mux.HandleFunc("POST /api/tasks/{id}/upload", s.handleUploadFiles)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleStreamOutput)
	mux.HandleFunc("GET /api/tasks/{id}/size", s.handleGetSize)
	mux.HandleFunc("POST /api/tasks/{id}/resize", s.handleResize)
	mux.HandleFunc("POST /api/tasks/{id}/archive", s.handleArchiveTask)
	mux.HandleFunc("POST /api/tasks/{id}/unarchive", s.handleUnarchiveTask)
	mux.HandleFunc("POST /api/tasks/{id}/rename", s.handleRenameTask)
	mux.HandleFunc("POST /api/tasks/{id}/status", s.handleSetStatus)
	mux.HandleFunc("POST /api/tasks/{id}/fork", s.handleForkTask)

	mux.HandleFunc("POST /api/sessions/stop-all", s.handleStopAll)
	mux.HandleFunc("POST /api/maintenance/prune-completed", s.handlePruneCompleted)
	mux.HandleFunc("GET /api/projects/full", s.handleListProjectsFull)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("PUT /api/projects/{name}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{name}", s.handleDeleteProject)
	mux.HandleFunc("GET /api/backends", s.handleListBackends)
	mux.HandleFunc("POST /api/backends", s.handleCreateBackend)
	mux.HandleFunc("PUT /api/backends/{name}", s.handleUpdateBackend)
	mux.HandleFunc("DELETE /api/backends/{name}", s.handleDeleteBackend)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("GET /api/sessions/state", s.handleSessionState)
	mux.HandleFunc("GET /api/sessions/{id}/pending-restart", s.handleHasPendingRestart)

	// Raw endpoints — return full model.Task / model.ScheduledTask shapes
	// (vs the lossy taskJSON / scheduleJSON used by the SPA). Added in
	// phase 2/3 of the remote-TUI work so apistore can faithfully
	// implement the tui store.Store interface without dropping fields like
	// BaseBranch, Result, AgentPID, SessionID, etc.
	mux.HandleFunc("GET /api/tasks-raw", s.handleListTasksRaw)
	mux.HandleFunc("GET /api/tasks/{id}/raw", s.handleGetTaskRaw)
	mux.HandleFunc("PUT /api/tasks/{id}/raw", s.handleUpdateTaskRaw)
	mux.HandleFunc("POST /api/tasks-raw", s.handleAddTaskRaw)
	mux.HandleFunc("GET /api/schedules/{id}/raw", s.handleGetScheduleRaw)
	mux.HandleFunc("GET /api/tasks/{id}/clipboard", s.handleClipboardGet)
	mux.HandleFunc("POST /api/tasks/{id}/clipboard", s.handleClipboardSet)
	mux.HandleFunc("DELETE /api/tasks/{id}/clipboard", s.handleClipboardClear)
	mux.HandleFunc("GET /api/tasks/{id}/git/status", s.handleGitStatus)
	mux.HandleFunc("GET /api/tasks/{id}/git/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/tasks/{id}/files", s.handleFileTree)
	// Session artifacts: list metadata + serve raw bytes (scoped to the
	// registered manifest set; see internal/api/artifacts.go). Authenticated
	// like the rest of /api/*.
	mux.HandleFunc("GET /api/tasks/{id}/artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /api/tasks/{id}/artifacts/{filename}", s.handleGetArtifact)
	mux.HandleFunc("GET /api/push/vapid-public-key", s.handleVapidPublicKey)
	mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("DELETE /api/push/subscribe/{id}", s.handlePushUnsubscribe)
	mux.HandleFunc("GET /api/push/subscriptions", s.handlePushList)
	mux.HandleFunc("POST /api/push/test", s.handlePushTest)
	mux.HandleFunc("GET /api/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.handleRevokeToken)
	// Plugin substrate: runtime MCP tool registration (PR 4). Plugins
	// (scope-tagged tokens) register and unregister their own tools; master
	// can drop any tool for operator cleanup.
	mux.HandleFunc("POST /api/mcp/tools", s.handleRegisterMCPTool)
	mux.HandleFunc("DELETE /api/mcp/tools/{name}", s.handleUnregisterMCPTool)
	mux.HandleFunc("GET /api/source-path", s.handleGetSourcePath)
	mux.HandleFunc("PUT /api/source-path", s.handleSetSourcePath)
	mux.HandleFunc("POST /api/update", s.handleUpdateSelf)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/logs/{name}", s.handleGetLog)
	mux.HandleFunc("GET /api/schedules", s.handleListSchedules)
	mux.HandleFunc("POST /api/schedules", s.handleCreateSchedule)
	mux.HandleFunc("PUT /api/schedules/{id}", s.handleUpdateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", s.handleDeleteSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/run", s.handleRunSchedule)

	// Plugin substrate (PR 2): event stream. Server-Sent Events with cursor
	// replay + resync on overflow. Accepts any authenticated token (master
	// or device); plugin-scoped tokens land here once PR 1 ships.
	mux.HandleFunc("GET /api/events/stream", s.handleEventsStream)

	// Inter-task messaging. Open to any authenticated token (single-tier
	// auth — see requireMaster doc for the master-only denylist).
	mux.HandleFunc("GET /api/tasks/{id}/inbox", s.handleListInbox)
	mux.HandleFunc("POST /api/tasks/{id}/inbox/ack", s.handleAckInbox)
	mux.HandleFunc("POST /api/tasks/{id}/messages", s.handleSendMessage)

	// Reliable pane-delivery (post/cancel). Open to any authenticated token —
	// plugins use plugin-scoped tokens to deliver pointer text to recipient tasks.
	mux.HandleFunc("POST /api/tasks/{id}/notify", s.handleNotify)
	mux.HandleFunc("DELETE /api/tasks/{id}/notify/{delivery_id}", s.handleCancelNotify)

	// Per-task sidecar metadata. PR 3 of the plugin substrate plan: a generic
	// k/v store keyed by (task_id, namespace, key) so plugins can annotate
	// tasks without piling new columns onto the tasks schema. Single-tier
	// auth: reads and writes are open to any authenticated token; scope tokens
	// are namespace-confined on write (see handlePutMeta / requireMaster doc).
	mux.HandleFunc("GET /api/tasks/{id}/meta", s.handleGetMeta)
	mux.HandleFunc("PUT /api/tasks/{id}/meta", s.handlePutMeta)

	// Plugin-registered settings sections (PR 7). POST takes a scope-tagged
	// token (the scope becomes the section's namespace); GET is open to any
	// authenticated request so the TUI can list registered sections. DELETE
	// requires either the owning scope or the master token. The submit
	// sub-route is the form-save proxy: the TUI POSTs the user-entered
	// values here, and the daemon forwards them to the plugin's
	// callback_url so cross-network egress is centralized.
	mux.HandleFunc("GET /api/plugins/settings/sections", s.handleListPluginSections)
	mux.HandleFunc("POST /api/plugins/settings/sections", s.handleRegisterPluginSection)
	mux.HandleFunc("DELETE /api/plugins/settings/sections/{scope}/{title}", s.handleUnregisterPluginSection)
	mux.HandleFunc("POST /api/plugins/settings/sections/{scope}/{title}/submit", s.handleSubmitPluginSectionValues)

	// Plugin-registered top-level views (PR 9). POST/GET/DELETE are master-only
	// today; see internal/api/plugin_views.go for the post-PR-1 swap TODO
	// that broadens auth to "master OR scope" once scope-tokens land.
	mux.HandleFunc("POST /api/plugins/views", s.handleCreatePluginView)
	mux.HandleFunc("GET /api/plugins/views", s.handleListPluginViews)
	mux.HandleFunc("DELETE /api/plugins/views/{id}", s.handleDeletePluginView)

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
		writeErr(w, http.StatusInternalServerError, "failed to load projects", err)
		return
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"projects": names})
}
