package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// handleListTasksRaw returns every task as a full model.Task (vs the lossy
// taskJSON shape /api/tasks emits for the SPA). The remote-TUI store adapter
// in internal/apistore uses this to mirror *db.DB.Tasks() faithfully.
//
// Master-only — exposes SessionID, AgentPID, Sandboxed, Result blob,
// BaseBranch, DependsOn, PlanSlug that the lossy /api/tasks deliberately
// strips. Device tokens (PWA) keep using /api/tasks; the TUI store adapter
// always operates under the master token, so this gate doesn't break it.
func (s *Server) handleListTasksRaw(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	tasks, err := s.db.Tasks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if tasks == nil {
		tasks = []*model.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// handleGetTaskRaw returns a single task as a full model.Task. Master-only
// for the same reason as handleListTasksRaw — SessionID and Result blob
// are intentionally not exposed to device tokens.
func (s *Server) handleGetTaskRaw(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleUpdateTaskRaw applies a full model.Task overwrite. Master-only — the
// remote TUI uses this to mirror *db.DB.Update for status flips, archive
// toggles, etc. The path ID and the body's ID must match.
//
// Worktree is locked to the existing DB value rather than the request body
// so a master-token holder can't poison the path with something outside the
// configured worktrees root. Same for Branch and BaseBranch — those would
// let the next delete operate on the wrong git repo. Status/Prompt/Result/
// Pinned/Archived/DependsOn/PlanSlug/AgentPID/SessionID etc. flow through
// because those are the fields the TUI legitimately updates.
func (s *Server) handleUpdateTaskRaw(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var task model.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if task.ID != id {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body id does not match path id"})
		return
	}
	// Pin worktree-related fields to the DB's existing values. A master
	// token holder who edits these could otherwise re-target the next
	// `git worktree remove` at an arbitrary path.
	existing, err := s.db.Get(id)
	if err != nil || existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	task.Worktree = existing.Worktree
	task.Branch = existing.Branch
	task.BaseBranch = existing.BaseBranch
	if err := s.db.Update(&task); err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, &task)
}

// handleAddTaskRaw inserts a model.Task row directly — for the rare TUI path
// (fork, schedule fire) that creates a task without going through the agent
// session lifecycle. Most fresh-task creation runs through POST /api/tasks
// which spawns a session; this raw path is for db.Add equivalents.
// Master-only.
func (s *Server) handleAddTaskRaw(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var task model.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := s.db.Add(&task); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, &task)
}

// handleGetScheduleRaw returns the schedule as a full model.ScheduledTask
// for the remote TUI store adapter.
func (s *Server) handleGetScheduleRaw(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := r.PathValue("id")
	sched, err := s.db.GetSchedule(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sched)
}
