package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/skills"
)

// --- Status ---

type statusResponse struct {
	OK       bool           `json:"ok"`
	Sessions sessionCounts  `json:"sessions"`
	Tasks    taskCounts     `json:"tasks"`
}

type sessionCounts struct {
	Running int `json:"running"`
	Idle    int `json:"idle"`
}

type taskCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	InReview   int `json:"in_review"`
	Complete   int `json:"complete"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	running, idle := s.runner.RunningAndIdle()

	tasks, err := s.db.Tasks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load tasks: " + err.Error()})
		return
	}
	var tc taskCounts
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		switch t.Status {
		case model.StatusPending:
			tc.Pending++
		case model.StatusInProgress:
			tc.InProgress++
		case model.StatusInReview:
			tc.InReview++
		case model.StatusComplete:
			tc.Complete++
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{
		OK:       true,
		Sessions: sessionCounts{Running: len(running), Idle: len(idle)},
		Tasks:    tc,
	})
}

// --- List Tasks ---

type taskJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Project      string `json:"project"`
	Branch       string `json:"branch,omitempty"`
	Backend      string `json:"backend,omitempty"`
	PRURL        string `json:"pr_url,omitempty"`
	Elapsed      string `json:"elapsed,omitempty"`
	CreatedAt    string `json:"created_at"`
	Archived     bool   `json:"archived,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
}

func taskToJSON(t *model.Task) taskJSON {
	return taskJSON{
		ID:           t.ID,
		Name:         t.Name,
		Status:       t.Status.String(),
		Project:      t.Project,
		Branch:       t.Branch,
		Backend:      t.Backend,
		PRURL:        t.PRURL,
		Elapsed:      t.ElapsedString(),
		CreatedAt:    t.CreatedAt.Format(time.RFC3339),
		Archived:     t.Archived,
		WorktreePath: t.Worktree,
		Prompt:       t.Prompt,
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.db.Tasks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load tasks: " + err.Error()})
		return
	}

	// Optional filters.
	statusFilter := r.URL.Query().Get("status")
	projectFilter := r.URL.Query().Get("project")
	// archived: "0" (default) excludes archived; "1" returns only archived;
	// "all" returns both.
	archivedFilter := r.URL.Query().Get("archived")

	result := make([]taskJSON, 0)
	for _, t := range tasks {
		switch archivedFilter {
		case "1":
			if !t.Archived {
				continue
			}
		case "all":
			// no filter
		default:
			if t.Archived {
				continue
			}
		}
		if statusFilter != "" && t.Status.String() != statusFilter {
			continue
		}
		if projectFilter != "" && t.Project != projectFilter {
			continue
		}
		result = append(result, taskToJSON(t))
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": result})
}

// --- Get Task ---

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, taskToJSON(task))
}

// --- Create Task ---

type createTaskReq struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskReq
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Project == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project is required"})
		return
	}
	if req.Prompt == "" && req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name or prompt is required"})
		return
	}
	name := req.Name
	if name == "" {
		// Generate name from prompt (first 40 chars, sanitized).
		name = sanitizeName(req.Prompt)
	}

	task, err := s.createTask(name, req.Prompt, req.Project, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     task.ID,
		"name":   task.Name,
		"status": task.Status.String(),
	})
}

// --- Stop Task ---

func (s *Server) handleStopTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	if err := s.runner.Stop(id); err != nil && !errors.Is(err, agent.ErrSessionNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	task.SetStatus(model.StatusInReview)
	s.db.Update(task) //nolint:errcheck

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// --- Resume Task ---

func (s *Server) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	if task.Status == model.StatusInProgress {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "task already running"})
		return
	}

	cfg := s.db.Config()
	resume := task.SessionID != ""

	sess, err := s.runner.Start(task, cfg, 24, 80, resume)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	s.db.Update(task) //nolint:errcheck

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "resumed",
		"pid":    task.AgentPID,
	})
}

// --- Delete Task ---

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	// Stop the session if running.
	_ = s.runner.Stop(id)

	if err := s.db.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Archive / Unarchive ---

func (s *Server) handleArchiveTask(w http.ResponseWriter, r *http.Request) {
	s.setArchive(w, r, true)
}

func (s *Server) handleUnarchiveTask(w http.ResponseWriter, r *http.Request) {
	s.setArchive(w, r, false)
}

func (s *Server) setArchive(w http.ResponseWriter, r *http.Request, archived bool) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	task.Archived = archived
	if err := s.db.Update(task); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archived": archived})
}

// --- Rename ---

type renameReq struct {
	Name string `json:"name"`
}

func (s *Server) handleRenameTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	var req renameReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	task.Name = name
	if err := s.db.Update(task); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": task.Name})
}

// --- Set Status ---

type statusSetReq struct {
	Status string `json:"status"`
}

func (s *Server) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	var req statusSetReq
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	var st model.Status
	switch req.Status {
	case "pending":
		st = model.StatusPending
	case "in_progress":
		st = model.StatusInProgress
	case "in_review":
		st = model.StatusInReview
	case "complete":
		st = model.StatusComplete
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown status: " + req.Status})
		return
	}
	task.SetStatus(st)
	if err := s.db.Update(task); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": task.Status.String()})
}

// --- Stop All ---

func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request) {
	// Destructive: master-only. Device tokens can stop individual tasks but
	// cannot halt every running agent in one call.
	if requireMaster(w, r) {
		return
	}
	s.runner.StopAll()
	// Mark every running task as in_review.
	tasks, err := s.db.Tasks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stopped := 0
	for _, t := range tasks {
		if t.Status == model.StatusInProgress {
			t.SetStatus(model.StatusInReview)
			s.db.Update(t) //nolint:errcheck
			stopped++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped})
}

// --- Fork ---

type forkReq struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
}

func (s *Server) handleForkTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := s.db.Get(id)
	if err != nil || src == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	var req forkReq
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = src.Name + "-fork"
	}
	prompt := req.Prompt
	if prompt == "" {
		prompt = src.Prompt
	}
	project := req.Project
	if project == "" {
		project = src.Project
	}
	if project == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project is required (source task has no project)"})
		return
	}
	task, err := s.createTask(name, prompt, project, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     task.ID,
		"name":   task.Name,
		"status": task.Status.String(),
	})
}

// --- Get Output ---

func (s *Server) handleGetOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Parse optional tail size (default 32KB, max 1MB).
	tailSize := 32 * 1024
	if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil && n > 0 {
		tailSize = min(n, 1<<20)
	}

	clean := r.URL.Query().Get("clean") == "1"

	// Try live session first.
	sess := s.runner.Get(id)
	if sess != nil {
		data := sess.RecentOutputTail(tailSize)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Source", "live")
		if clean {
			w.Write([]byte(sanitize.CleanPTYOutput(string(data)))) //nolint:errcheck
		} else {
			w.Write(data) //nolint:errcheck
		}
		return
	}

	// Fall back to session log file.
	logPath := agent.SessionLogPath(id)
	f, err := os.Open(logPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no output available"})
		return
	}
	defer f.Close()

	// Read the tail of the file.
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	offset := info.Size() - int64(tailSize)
	if offset < 0 {
		offset = 0
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Source", "log")
	if clean {
		f.Seek(offset, io.SeekStart) //nolint:errcheck
		raw, err := io.ReadAll(f)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Write([]byte(sanitize.CleanPTYOutput(string(raw)))) //nolint:errcheck
	} else {
		f.Seek(offset, io.SeekStart) //nolint:errcheck
		io.Copy(w, f)                //nolint:errcheck
	}
}

// --- Write Input ---

func (s *Server) handleWriteInput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	sess := s.runner.Get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active session"})
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if _, err := sess.WriteInput(data); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "bytes": strconv.Itoa(len(data))})
}

// --- PTY Size / Resize ---

type sizeResponse struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (s *Server) handleGetSize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.runner.Get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active session"})
		return
	}
	cols, rows := sess.PTYSize()
	writeJSON(w, http.StatusOK, sizeResponse{Cols: cols, Rows: rows})
}

type resizeReq struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.runner.Get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active session"})
		return
	}
	var req resizeReq
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Cols == 0 || req.Rows == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cols and rows must be > 0"})
		return
	}
	// Sanity bounds — terminals beyond these are pathological.
	if req.Cols > 1000 || req.Rows > 1000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cols/rows out of range"})
		return
	}
	if err := sess.Resize(req.Rows, req.Cols); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sizeResponse{Cols: int(req.Cols), Rows: int(req.Rows)})
}

// --- Stream Output (SSE) ---

func (s *Server) handleStreamOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	sess := s.runner.Get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active session"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Use a channelWriter so AddWriter's synchronous ring buffer replay
	// doesn't deadlock. The channel is large enough to hold the full 256KB
	// ring buffer replay (256KB / 4KB chunks = 64 items).
	cw := &channelWriter{ch: make(chan []byte, 128)}
	sess.AddWriter(cw)
	defer sess.RemoveWriter(cw)

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case data, ok := <-cw.ch:
			if !ok {
				fmt.Fprintf(w, "event: exit\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// channelWriter implements io.Writer by sending copies of written data to a
// buffered channel. This avoids the io.Pipe deadlock when AddWriter replays
// the ring buffer synchronously — the channel buffer absorbs the full replay.
type channelWriter struct {
	ch chan []byte
}

func (cw *channelWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case cw.ch <- cp:
		return len(p), nil
	default:
		// Channel full — drop data to avoid blocking the session's readLoop.
		return len(p), nil
	}
}

// --- Projects (full CRUD) ---

type projectJSON struct {
	Name    string                  `json:"name"`
	Path    string                  `json:"path"`
	Branch  string                  `json:"branch,omitempty"`
	Backend string                  `json:"backend,omitempty"`
	Sandbox *projectSandboxOverride `json:"sandbox,omitempty"`
}

// projectSandboxOverride is a JSON-friendly view of config.ProjectSandboxConfig.
// `enabled` is a *bool because nil means "inherit global"; the JSON encoder
// emits `null` for that, which the SPA renders as an "Inherit" radio.
type projectSandboxOverride struct {
	Enabled    *bool    `json:"enabled"`
	DenyRead   []string `json:"deny_read"`
	ExtraWrite []string `json:"extra_write"`
}

func projectToJSON(name string, p config.Project) projectJSON {
	out := projectJSON{Name: name, Path: p.Path, Branch: p.Branch, Backend: p.Backend}
	if p.Sandbox.Enabled != nil || len(p.Sandbox.DenyRead) > 0 || len(p.Sandbox.ExtraWrite) > 0 {
		out.Sandbox = &projectSandboxOverride{
			Enabled:    p.Sandbox.Enabled,
			DenyRead:   stringsOrEmpty(p.Sandbox.DenyRead),
			ExtraWrite: stringsOrEmpty(p.Sandbox.ExtraWrite),
		}
	}
	return out
}

func projectFromJSON(req projectJSON) config.Project {
	out := config.Project{
		Path:    req.Path,
		Branch:  req.Branch,
		Backend: req.Backend,
	}
	if req.Sandbox != nil {
		out.Sandbox.Enabled = req.Sandbox.Enabled
		out.Sandbox.DenyRead = req.Sandbox.DenyRead
		out.Sandbox.ExtraWrite = req.Sandbox.ExtraWrite
	}
	return out
}

func (s *Server) handleListProjectsFull(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.Projects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]projectJSON, 0, len(projects))
	for name, p := range projects {
		out = append(out, projectToJSON(name, p))
	}
	// stable order
	sortByName := func(a, b projectJSON) bool { return a.Name < b.Name }
	sortProjects(out, sortByName)
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func sortProjects(items []projectJSON, less func(a, b projectJSON) bool) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req projectJSON
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and path are required"})
		return
	}
	if err := s.db.SetProject(req.Name, projectFromJSON(req)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	name := r.PathValue("name")
	var req projectJSON
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Path is required on update too.
	if strings.TrimSpace(req.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	if err := s.db.SetProject(name, projectFromJSON(req)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Name = name
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := s.db.DeleteProject(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

// --- Backends (full CRUD) ---

type backendJSON struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	PromptFlag string `json:"prompt_flag,omitempty"`
}

func (s *Server) handleListBackends(w http.ResponseWriter, r *http.Request) {
	backends, err := s.db.Backends()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]backendJSON, 0, len(backends))
	for name, b := range backends {
		out = append(out, backendJSON{Name: name, Command: b.Command, PromptFlag: b.PromptFlag})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backends": out})
}

func (s *Server) handleCreateBackend(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req backendJSON
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and command are required"})
		return
	}
	if err := s.db.SetBackend(req.Name, config.Backend{
		Command:    req.Command,
		PromptFlag: req.PromptFlag,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleUpdateBackend(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	name := r.PathValue("name")
	var req backendJSON
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	if err := s.db.SetBackend(name, config.Backend{
		Command:    req.Command,
		PromptFlag: req.PromptFlag,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Name = name
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteBackend(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := s.db.DeleteBackend(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

// --- Git status / diff / files ---

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil || task.Worktree == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task or worktree not found"})
		return
	}
	msg := gitutil.FetchGitStatus(task.ID, task.Worktree)
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil || task.Worktree == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task or worktree not found"})
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" || strings.Contains(path, "..") || filepath.IsAbs(path) {
		// Reject absolute paths — gitutil.FetchFileDiff falls back to
		// `git diff --no-index <path>` which would otherwise read arbitrary
		// files (e.g. /etc/passwd) for files not in the worktree.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid path query param required"})
		return
	}
	msg := gitutil.FetchFileDiff(task.ID, task.Worktree, path)
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleFileTree(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil || task.Worktree == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task or worktree not found"})
		return
	}
	dir := r.URL.Query().Get("dir")
	if strings.Contains(dir, "..") || filepath.IsAbs(dir) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dir"})
		return
	}
	msg := gitutil.FetchDirFiles(task.ID, task.Worktree, dir)
	writeJSON(w, http.StatusOK, msg)
}

// --- Skills ---

type skillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	prefix := r.URL.Query().Get("prefix")

	var extraDirs []string
	if project != "" {
		projects, err := s.db.Projects()
		if err != nil {
			log.Printf("[api] handleListSkills: failed to load projects: %v", err)
		} else if p, ok := projects[project]; ok && p.Path != "" {
			extraDirs = []string{filepath.Join(p.Path, ".claude", "skills")}
		}
	}

	items := skills.LoadSkills(extraDirs)
	if prefix != "" {
		items = skills.FilterSkills(items, prefix)
	}

	result := make([]skillJSON, len(items))
	for i, it := range items {
		result[i] = skillJSON{Name: it.Name, Description: it.Description}
	}

	writeJSON(w, http.StatusOK, map[string]any{"skills": result})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[api] json encode error: %v", err)
	}
}

func sanitizeName(prompt string) string {
	// Take first 40 runes, replace newlines with spaces.
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' {
			runes[i] = ' '
		}
	}
	return string(runes)
}
