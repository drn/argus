package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// worktreeWithinRoot reports whether p is a path strictly beneath the
// canonical worktrees root (~/.argus/worktrees). Used to bound the untrusted
// Worktree field on raw task inserts: under single-tier auth any token can
// POST /api/tasks-raw, and a task's Worktree later flows to
// RemoveWorktreeAndBranch (os.RemoveAll) on delete, so an out-of-root path
// must never be persisted. Empty paths are handled by the caller (allowed —
// no worktree means no cleanup). The root itself (rel ".") is rejected — a
// real task worktree is always a <project>/<task> subdirectory, never the
// root, and accepting the root would let a delete target the whole tree.
func worktreeWithinRoot(p string) bool {
	root := filepath.Join(db.DataDir(), "worktrees")
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// handleListTasksRaw returns every task as a full model.Task (vs the lossy
// taskJSON shape /api/tasks emits for the SPA). The remote-TUI store adapter
// in internal/apistore uses this to mirror *db.DB.Tasks() faithfully.
//
// Exposes SessionID, AgentPID, Sandboxed, Result blob, BaseBranch, DependsOn,
// PlanSlug that the lossy /api/tasks deliberately strips. The SPA keeps using
// /api/tasks; the remote-TUI store adapter uses this. Single-tier auth: any
// authenticated token may read it (no credentials in the model.Task shape).
func (s *Server) handleListTasksRaw(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.db.Tasks()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	if tasks == nil {
		tasks = []*model.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// handleGetTaskRaw returns a single task as a full model.Task. Open to any
// authenticated token, same as handleListTasksRaw.
func (s *Server) handleGetTaskRaw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleUpdateTaskRaw applies a full model.Task overwrite. The remote TUI
// uses this to mirror *db.DB.Update for status flips, archive toggles, etc.
// The path ID and the body's ID must match. Open to any authenticated token.
//
// Worktree is locked to the existing DB value rather than the request body
// so the caller can't poison the path with something outside the configured
// worktrees root. Same for Branch and BaseBranch — those would let the next
// delete operate on the wrong git repo. Status/Prompt/Result/Pinned/Archived/
// DependsOn/PlanSlug/AgentPID/SessionID etc. flow through because those are
// the fields the TUI legitimately updates.
func (s *Server) handleUpdateTaskRaw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var task model.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if task.ID != id {
		writeErr(w, http.StatusBadRequest, "body id does not match path id", nil)
		return
	}
	// Pin worktree-related fields to the DB's existing values. A master
	// token holder who edits these could otherwise re-target the next
	// `git worktree remove` at an arbitrary path.
	existing, err := s.db.Get(id)
	if err != nil || existing == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}
	task.Worktree = existing.Worktree
	task.Branch = existing.Branch
	task.BaseBranch = existing.BaseBranch
	if err := s.db.Update(&task); err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			writeErr(w, http.StatusNotFound, "", err)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	writeJSON(w, http.StatusOK, &task)
}

// handleAddTaskRaw inserts a model.Task row directly — for the rare TUI path
// (fork, schedule fire) that creates a task without going through the agent
// session lifecycle. Most fresh-task creation runs through POST /api/tasks
// which spawns a session; this raw path is for db.Add equivalents.
// Open to any authenticated token.
func (s *Server) handleAddTaskRaw(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var task model.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	// The body is untrusted (single-tier auth opens this to any token). Unlike
	// handleUpdateTaskRaw, there is no existing row to pin Worktree against, so
	// reject any non-empty Worktree outside the canonical worktrees root — that
	// field later drives os.RemoveAll on delete. Legit callers (the no-worktree
	// new-task path, fork, schedule-fire) leave it empty or supply an in-root
	// path.
	if task.Worktree != "" && !worktreeWithinRoot(task.Worktree) {
		writeErr(w, http.StatusBadRequest, "worktree must be within the worktrees root", nil)
		return
	}
	if err := s.db.Add(&task); err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	writeJSON(w, http.StatusCreated, &task)
}

// handleGetScheduleRaw returns the schedule as a full model.ScheduledTask
// for the remote TUI store adapter.
func (s *Server) handleGetScheduleRaw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sched, err := s.db.GetSchedule(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeErr(w, http.StatusNotFound, "", err)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	writeJSON(w, http.StatusOK, sched)
}
