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
	"strconv"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
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

	tasks := s.db.Tasks()
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Project   string `json:"project"`
	Branch    string `json:"branch,omitempty"`
	Backend   string `json:"backend,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	Elapsed   string `json:"elapsed,omitempty"`
	CreatedAt string `json:"created_at"`
}

func taskToJSON(t *model.Task) taskJSON {
	return taskJSON{
		ID:        t.ID,
		Name:      t.Name,
		Status:    t.Status.String(),
		Project:   t.Project,
		Branch:    t.Branch,
		Backend:   t.Backend,
		PRURL:     t.PRURL,
		Elapsed:   t.ElapsedString(),
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.db.Tasks()

	// Optional filters.
	statusFilter := r.URL.Query().Get("status")
	projectFilter := r.URL.Query().Get("project")

	var result []taskJSON
	for _, t := range tasks {
		if t.Archived {
			continue
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

// --- Get Output ---

func (s *Server) handleGetOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Parse optional tail size (default 32KB, max 1MB).
	tailSize := 32 * 1024
	if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil && n > 0 {
		tailSize = min(n, 1<<20)
	}

	// Try live session first.
	sess := s.runner.Get(id)
	if sess != nil {
		data := sess.RecentOutputTail(tailSize)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Source", "live")
		w.Write(data) //nolint:errcheck
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
	f.Seek(offset, io.SeekStart) //nolint:errcheck
	io.Copy(w, f)                //nolint:errcheck
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
