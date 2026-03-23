package api

import (
	"embed"
	"net/http"
	"sort"
)

//go:embed static/index.html
var dashboardHTML embed.FS

// routes returns the HTTP mux with all API endpoints registered.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Dashboard — served without auth so the page can load and prompt for token.
	mux.HandleFunc("GET /", s.handleDashboard)

	// API endpoints — auth is applied by the middleware wrapper in ListenAndServe,
	// but the dashboard route is excluded from auth below.
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/tasks/{id}/stop", s.handleStopTask)
	mux.HandleFunc("POST /api/tasks/{id}/resume", s.handleResumeTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("GET /api/tasks/{id}/output", s.handleGetOutput)
	mux.HandleFunc("POST /api/tasks/{id}/input", s.handleWriteInput)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleStreamOutput)

	return mux
}

// handleDashboard serves the embedded HTML dashboard.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardHTML.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// handleListProjects returns the list of configured project names.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects := s.db.Projects()
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"projects": names})
}
