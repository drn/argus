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
	"github.com/drn/argus/internal/links"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// --- Status ---

type statusResponse struct {
	OK       bool          `json:"ok"`
	Sessions sessionCounts `json:"sessions"`
	Tasks    taskCounts    `json:"tasks"`
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

// taskJSON is the wire shape returned by /api/tasks*.
//
// Status is the persisted DB status. Idle is a runtime-derived flag that is
// true only when Status == in_progress and the agent session is missing or
// waiting for input. omitempty drops idle:false from the JSON; the SPA reads
// missing fields as falsy, which matches the intended contract (no idle field
// == not idle).
type taskJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Idle         bool   `json:"idle,omitempty"`
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

// taskRuntimeState carries non-persisted, derived-at-request-time fields used
// to populate taskJSON. Group runtime flags here so the taskToJSON signature
// doesn't grow into a positional bool chain when more flags are added.
type taskRuntimeState struct {
	Idle bool
}

func taskToJSON(t *model.Task, rt taskRuntimeState) taskJSON {
	return taskJSON{
		ID:           t.ID,
		Name:         t.Name,
		Status:       t.Status.String(),
		Idle:         rt.Idle,
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

// computeRuntimeState derives the per-request runtime state for a task.
// Mirrors the TUI's drawTaskRow rule: an InProgress task is idle when it has
// no live session (running map miss) or its session is waiting for input
// (idle map hit). Non-InProgress tasks are never idle.
func computeRuntimeState(t *model.Task, runningSet, idleSet map[string]bool) taskRuntimeState {
	if t.Status != model.StatusInProgress {
		return taskRuntimeState{}
	}
	return taskRuntimeState{Idle: !runningSet[t.ID] || idleSet[t.ID]}
}

func (s *Server) sessionStateMaps() (runningSet, idleSet map[string]bool) {
	running, idle := s.runner.RunningAndIdle()
	runningSet = make(map[string]bool, len(running))
	for _, id := range running {
		runningSet[id] = true
	}
	idleSet = make(map[string]bool, len(idle))
	for _, id := range idle {
		idleSet[id] = true
	}
	return runningSet, idleSet
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

	runningSet, idleSet := s.sessionStateMaps()

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
		result = append(result, taskToJSON(t, computeRuntimeState(t, runningSet, idleSet)))
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
	runningSet, idleSet := s.sessionStateMaps()
	writeJSON(w, http.StatusOK, taskToJSON(task, computeRuntimeState(task, runningSet, idleSet)))
}

// --- Create Task ---

// validateBackend returns nil if name is empty (caller will fall back to the
// configured default) or matches a configured backend. Returns an error if the
// caller asked for a backend that isn't in the config — fail fast at the API
// boundary instead of letting agent.CreateAndStart roll back the worktree
// after the fact.
func (s *Server) validateBackend(name string) error {
	if name == "" {
		return nil
	}
	cfg := s.db.Config()
	if _, ok := cfg.Backends[name]; !ok {
		return fmt.Errorf("backend %q not configured", name)
	}
	return nil
}

type createTaskReq struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
	// Backend overrides the default backend for this task. Empty = use the
	// per-project / global default. Validated against the configured backends
	// before the task is created.
	Backend string `json:"backend"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	// Multipart bodies carry user-uploaded attachments alongside the task
	// fields. They must be handled differently because (a) we need to copy
	// files into the worktree before the agent starts, and (b) the body
	// can be much larger than 1MB.
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "multipart/form-data") {
		s.handleCreateTaskMultipart(w, r)
		return
	}

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
	if err := s.validateBackend(req.Backend); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	autoName := req.Name == ""
	name := req.Name
	if name == "" {
		// Generate name from prompt (first 40 chars, sanitized).
		name = sanitizeName(req.Prompt)
	}

	task, err := s.createTask(name, req.Prompt, req.Project, req.Backend, autoName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// task.Name here is the regex slug. When autoName is true, a Haiku
	// rename fires asynchronously after this response — clients that re-list
	// or stream tasks will see the updated name within a few seconds.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     task.ID,
		"name":   task.Name,
		"status": task.Status.String(),
	})
}

// handleCreateTaskMultipart handles POST /api/tasks with a multipart body —
// task fields (name/prompt/project) plus zero or more file parts. Attachments
// are written to <worktree>/.context/ before the session starts; their paths
// are appended to the prompt so the agent sees them on its first turn.
//
// Bypasses s.createTask (the daemon-injected callback) and calls
// agent.CreateAndStart directly because the callback's narrow signature has
// no place for attachments and the API server runs in the daemon process so
// it has direct access to db/runner.
func (s *Server) handleCreateTaskMultipart(w http.ResponseWriter, r *http.Request) {
	// 50MB total cap + headroom for the multipart envelope and text fields.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentTotalBytes+1<<20)

	name, prompt, project, backend, atts, err := parseMultipartTaskForm(r)
	if err != nil {
		writeJSON(w, statusForUploadErr(err), map[string]string{"error": err.Error()})
		return
	}
	if project == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project is required"})
		return
	}
	if prompt == "" && name == "" && len(atts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, prompt, or files required"})
		return
	}
	if err := s.validateBackend(backend); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// autoName fires only when name was synthesized from prompt — not from
	// an attachment filename (which is already meaningful) and not when the
	// user typed a name explicitly.
	autoName := name == "" && prompt != ""
	if name == "" {
		if prompt != "" {
			name = sanitizeName(prompt)
		} else {
			name = sanitizeName(atts[0].Name)
		}
	}

	task, _, err := agent.CreateAndStart(s.db, s.runner, agent.CreateInput{
		Name:        name,
		Prompt:      prompt,
		Project:     project,
		Backend:     backend,
		Attachments: atts,
		AutoName:    autoName,
	})
	if err != nil {
		uxlog.Log("[uploads] create task failed name=%q project=%q files=%d err=%v", name, project, len(atts), err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	uxlog.Log("[uploads] task created id=%s name=%q files=%d", task.ID, task.Name, len(atts))
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
		// Only refuse when the agent is actively working. Idle in_progress
		// tasks (no live session OR session.IsIdle()) are exactly what the
		// PWA's "Resume" prompt targets — the user opened a quiet task and
		// wants to wake / restart it. StartOrReattach below handles both
		// sub-cases: returns the existing handle for live-but-idle, or
		// spawns fresh for ghost-in_progress.
		//
		// Note: this is a non-locked check followed by an unlocked
		// StartOrReattach. A session can flip between non-idle and exited
		// in the gap; the worst case is a harmless reattach that
		// immediately spawns fresh. Argus is single-user / single-daemon
		// so the race window has no real-world impact, but don't tighten
		// the check to atomic without auditing the StartOrReattach call
		// sites that share the same pattern.
		if sess := s.runner.Get(task.ID); sess != nil && !sess.IsIdle() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task already running"})
			return
		}
	}

	// StartOrReattach handles the desync case where the daemon still owns a
	// live PTY but the DB row drifted off in_progress (manual edit,
	// post-reconcile state). Calling Start directly there would fail with
	// "session already exists for task X". On reattach we re-sync the row
	// to in_progress so the PWA can attach the terminal stream and other
	// observers see consistent state.
	cfg := s.db.Config()
	resume := task.SessionID != ""
	prevStatus := task.Status

	sess, reattached, err := s.runner.StartOrReattach(task, cfg, 24, 80, resume)
	if err != nil {
		uxlog.Log("[api] resume: start failed task=%s status=%s err=%v", id, prevStatus, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	s.db.Update(task) //nolint:errcheck

	if reattached {
		uxlog.Log("[api] resume: healed task=%s pre_status=%s pid=%d", id, prevStatus, task.AgentPID)
	} else {
		uxlog.Log("[api] resume: started task=%s pid=%d resume=%t", id, task.AgentPID, resume)
	}

	resp := map[string]any{
		"status": "resumed",
		"pid":    task.AgentPID,
	}
	if reattached {
		resp["healed"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Delete Task ---

// handleDeleteTask is a per-task destructive endpoint. Auth: requires a valid
// token but NOT master — same tier as handleStopTask, since per-task ops are
// expected from the mobile PWA. requireMaster gates apply only to cross-task
// or config-mutating endpoints (handleStopAll, project/backend/token CRUD).
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	// Stop the session if running.
	_ = s.runner.Stop(id)

	// Remove session log file.
	os.Remove(agent.SessionLogPath(id)) //nolint:errcheck,gosec // G304: id was validated by db.Get above; SessionLogPath roots at ~/.argus/sessions/<id>.log

	if err := s.db.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Clean up worktree and branch in background — git operations can take seconds.
	// Mirrors tui.App.deleteTask so worktrees don't linger as orphans until the next
	// completed-task prune sweep.
	cfg := s.db.Config()
	worktree, branch := task.Worktree, task.Branch
	uxlog.Log("[api] delete: task=%s name=%q worktree=%q branch=%q", id, task.Name, worktree, branch)
	go func() {
		repoDir := agent.ResolveDir(task, cfg)
		if worktree != "" {
			agent.RemoveWorktreeAndBranch(worktree, branch, repoDir)
		} else if branch != "" && repoDir != "" {
			agent.DeleteBranch(repoDir, branch)
			agent.DeleteRemoteBranch(repoDir, branch)
		}
	}()

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
	// Targeted rename — avoids racing concurrent status changes from the
	// agent process. Mirrors the MCP task_rename and TUI rename modal paths.
	if err := s.db.Rename(task.ID, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
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
	// Fork name is structured ("<src>-fork" or user-typed); never auto-rename.
	// Forks inherit the source task's backend so the new task starts with the
	// same agent rather than silently defaulting back to the global setting.
	task, err := s.createTask(name, prompt, project, src.Backend, false)
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
	// 1MB ceiling is mirrored on the SPA side as STATIC_OUTPUT_MAX_BYTES
	// (currently 512KB) in static/index.html. If the SPA constant is bumped
	// past 1<<20 the request silently truncates here — bump this cap in
	// lockstep, otherwise scrollback reappears short.
	tailSize := 32 * 1024
	if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil && n > 0 {
		tailSize = min(n, 1<<20)
	}

	clean := r.URL.Query().Get("clean") == "1"

	// Always read from the on-disk session log when available, even for live
	// sessions. The log file holds the complete history; the in-memory ring
	// buffer only retains the last 256KB. For active in_progress tasks the
	// SPA pairs this read with /stream?since=<X-Output-Total>, where the
	// /stream endpoint replays only the (tiny) ring delta between this read
	// and the live attach. Together they give the client full history with
	// no gap and no duplicates.
	logPath := agent.SessionLogPath(id)
	// id comes from the URL path but is constrained: SessionLogPath does
	// filepath.Join(~/.argus/sessions, id+".log"), and `id` originates as
	// a server-generated nanosecond timestamp string (see model.Task.ID).
	// A `..` segment in the URL would produce a path like ".._.log" inside
	// the sessions dir, not an escape. Single-user local daemon, but the
	// invariant should be held even if a future feature accepts user IDs.
	f, err := os.Open(logPath) //nolint:gosec // logPath rooted at ~/.argus/sessions; id is server-generated
	if err != nil {
		// Live session present but log unavailable (best-effort log creation
		// failed at session start) — fall back to the ring buffer so the
		// client sees the recent tail rather than a 404.
		if sess := s.runner.Get(id); sess != nil {
			// Atomic snapshot: data and ringTotal MUST come from the same
			// lock acquisition. Reading them in two calls lets readLoop
			// advance ringTotal past the bytes in data, leaving the client
			// with a since-cursor that skips bytes never delivered.
			data, ringTotal := sess.RecentOutputTailWithTotal(tailSize)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Source", "ring")
			// data is the ring tail and ringTotal is the high-water mark; the
			// client now has bytes [ringTotal-len(data) .. ringTotal]. Setting
			// since=ringTotal on /stream attaches live with no replay (the
			// caller-already-has-it region matches exactly). Any bytes the
			// session writes between this header and the /stream open are
			// covered by AddWriterFrom's same-lock replay.
			w.Header().Set("X-Output-Total", strconv.FormatUint(ringTotal, 10))
			if clean {
				w.Write([]byte(sanitize.CleanPTYOutput(string(data)))) //nolint:errcheck,gosec // text/plain endpoint; bytes are PTY output for the local single-user daemon, not HTML
			} else {
				w.Write(data) //nolint:errcheck,gosec // text/plain endpoint; bytes are PTY output for the local single-user daemon, not HTML
			}
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no output available"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	fileSize := info.Size()
	startOff := fileSize - int64(tailSize)
	if startOff < 0 {
		startOff = 0
	}
	// Always >= 0 by construction (startOff is clamped to [0, fileSize]).
	// The same is asserted indirectly by io.CopyN below — it errors on a
	// negative count, but we never produce one here.
	bytesToRead := fileSize - startOff

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// X-Output-Total advertises the byte position the client should pass back
	// as ?since=<n> on the /stream endpoint to resume without overlap. We
	// pin it to the file size we observed under Stat() and bound the body
	// read to the same value (io.CopyN), so the count of bytes returned ==
	// X-Output-Total - startOff exactly. Without the bound, a live session
	// growing the file mid-read would leave the client's `since` short of
	// the bytes they actually have.
	w.Header().Set("X-Output-Total", strconv.FormatInt(fileSize, 10))
	if sess := s.runner.Get(id); sess != nil {
		w.Header().Set("X-Source", "live")
	} else {
		w.Header().Set("X-Source", "log")
	}

	if _, err := f.Seek(startOff, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if clean {
		raw, err := io.ReadAll(io.LimitReader(f, bytesToRead))
		if err != nil {
			// Headers (including X-Output-Total) are already sent; we
			// can't downgrade to 500. Returning silently truncates the
			// 200 response; the SPA's `since` will then point past the
			// bytes actually delivered. Rare in practice — io.ReadAll
			// from a local file fails only on OS errors — but worth
			// flagging if anyone hits the truncation symptom.
			return
		}
		w.Write([]byte(sanitize.CleanPTYOutput(string(raw)))) //nolint:errcheck
	} else {
		io.CopyN(w, f, bytesToRead) //nolint:errcheck
	}
}

// --- Get Links ---

// linksReadCap bounds the bytes we scan for URLs. The full session log can be
// many MB on long-running tasks; capping at 1MB matches the upper bound of
// the /output endpoint and keeps the worst-case allocation predictable.
const linksReadCap = 1 << 20

// handleGetLinks returns http/https URLs extracted from the task's terminal
// output (live ring buffer if running, otherwise the on-disk session log).
// Mirrors the TUI's ctrl+l fuzzy link picker so the web app can offer the
// same "Open link" affordance.
func (s *Server) handleGetLinks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var raw []byte
	// Live session: the full ring buffer is bounded by RingBuffer capacity,
	// so we don't need to cap further here.
	if sess := s.runner.Get(id); sess != nil {
		raw = sess.RecentOutputTail(linksReadCap)
	} else {
		// Fallback: read the tail of the session log file. Older content
		// beyond linksReadCap is dropped so a multi-MB log doesn't pin
		// memory just to enumerate links.
		logPath := agent.SessionLogPath(id)
		f, err := os.Open(logPath) //nolint:gosec // logPath is filepath.Join(~/.argus/sessions, id+".log"); same pattern as handleGetOutput
		if err != nil {
			// Intentionally 200 + empty list (vs handleGetOutput's 404):
			// the picker's caller just wants to render "No links found"
			// uniformly, whether the task doesn't exist or simply hasn't
			// emitted a URL yet. A 404 would force the JS into a bespoke
			// error path for what is, from the user's perspective, the
			// same outcome.
			writeJSON(w, http.StatusOK, map[string]any{"links": []links.Link{}})
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		offset := info.Size() - int64(linksReadCap)
		if offset < 0 {
			offset = 0
		}
		f.Seek(offset, io.SeekStart) //nolint:errcheck
		raw, err = io.ReadAll(f)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	out := links.Extract(string(raw))
	if out == nil {
		out = []links.Link{}
	}
	uxlog.Log("[api] task=%s links extracted: %d", id, len(out))
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
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

	// Optional resume cursor. Clients first read /output (which returns
	// X-Output-Total: N from the on-disk log size) and then open this stream
	// with ?since=N. AddWriterFrom replays only the ring delta between N and
	// the current total under a single lock acquisition, then attaches live
	// — no overlap with the disk-log bytes the client already has, no gap.
	// Absent or invalid `since` defaults to 0, which replays the full ring
	// (matches the legacy AddWriter behaviour). `since` > currentTotal is
	// also valid: AddWriterFrom skips the replay block entirely and attaches
	// live — the client is "ahead" of the ring (e.g., reconnected with a
	// stale-cached cursor), so no replay is the correct outcome.
	var since uint64
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			since = n
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Use a channelWriter so AddWriterFrom's synchronous ring buffer replay
	// doesn't block readLoop. The channel is large enough to hold the full
	// 256KB ring buffer replay (256KB / 4KB chunks = 64 items), and Write
	// drops on full to keep the lock-held replay non-blocking.
	cw := &channelWriter{ch: make(chan []byte, 128)}
	sess.AddWriterFrom(cw, since)
	defer sess.RemoveWriter(cw)

	// Subscribe to agent-staged clipboard updates for this task. Any change
	// (set or clear) queues a `clipboard` SSE event. The subscriber callback
	// runs on the goroutine that called Set/Clear — must not block, so a
	// buffered channel + drop-on-full keeps the producer fast. text and
	// present travel as a single struct so a partial drop can never desync
	// the two halves (an earlier two-channel design could pair a fresh text
	// with a stale present flag during bursty updates).
	clipCh := make(chan clipEvent, 16)
	var unsubClip func()
	if s.clipboard != nil {
		unsubClip = s.clipboard.Subscribe(id, func(text string) {
			select {
			case clipCh <- clipEvent{text: text, present: text != ""}:
			default:
			}
		})
		defer unsubClip()
		// Emit current state on connect so a freshly-opened tab catches a
		// payload that was staged before the SSE was established.
		if text, ok := s.clipboard.Get(id); ok {
			fmt.Fprintf(w, "event: clipboard\ndata: %s\n\n", encodeClipboardEvent(text, true)) //nolint:errcheck
			flusher.Flush()
		}
	}

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
		case ev := <-clipCh:
			fmt.Fprintf(w, "event: clipboard\ndata: %s\n\n", encodeClipboardEvent(ev.text, ev.present)) //nolint:errcheck
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// clipEvent bundles the text and presence flag for a clipboard SSE update so
// they travel through a single channel and can never desync under burst load.
type clipEvent struct {
	text    string
	present bool
}

// encodeClipboardEvent renders the JSON body of a clipboard SSE event.
// `{"text":"…"}` when a payload is present, `{"cleared":true}` otherwise.
func encodeClipboardEvent(text string, present bool) string {
	if !present {
		return `{"cleared":true}`
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return `{"cleared":true}`
	}
	return string(body)
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
	filter := r.URL.Query().Get("filter")

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
	if filter != "" {
		items = skills.FilterSkills(items, filter)
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
