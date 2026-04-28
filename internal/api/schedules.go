package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// maxScheduleBodyBytes caps schedule create/update JSON bodies. Prompts can
// be multi-line but anything over 1 MB is suspicious and a slow/large body
// would otherwise tie up a goroutine for the full ReadTimeout window.
const maxScheduleBodyBytes = 1 << 20

// ScheduleRunner is the subset of *scheduler.Scheduler that the API needs.
// Defined as an interface so the api package doesn't depend on the scheduler
// package (which depends on db, model, uxlog), keeping the import graph
// shallow and tests lightweight.
type ScheduleRunner interface {
	RunNow(id string) (*model.Task, error)
}

// SetScheduler wires a scheduler into the API so /run-now can fire schedules
// out-of-cycle. Called by the daemon after both the scheduler and API are
// constructed.
func (s *Server) SetScheduler(sch ScheduleRunner) {
	s.scheduler = sch
}

// scheduleJSON is the wire shape returned by /api/schedules*.
type scheduleJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Project    string `json:"project"`
	Prompt     string `json:"prompt"`
	Backend    string `json:"backend,omitempty"`
	Schedule   string `json:"schedule"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at"`
	LastRunAt  string `json:"last_run_at,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	LastTaskID string `json:"last_task_id,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func toScheduleJSON(s *model.ScheduledTask) scheduleJSON {
	js := scheduleJSON{
		ID:         s.ID,
		Name:       s.Name,
		Project:    s.Project,
		Prompt:     s.Prompt,
		Backend:    s.Backend,
		Schedule:   s.Schedule,
		Enabled:    s.Enabled,
		LastTaskID: s.LastTaskID,
		LastError:  s.LastError,
	}
	if !s.CreatedAt.IsZero() {
		js.CreatedAt = s.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !s.LastRunAt.IsZero() {
		js.LastRunAt = s.LastRunAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !s.NextRunAt.IsZero() {
		js.NextRunAt = s.NextRunAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return js
}

// scheduleRequest is the wire shape for create/update bodies. All fields are
// pointers on update so the SPA can do partial updates (e.g. toggle enabled
// without resending the prompt).
type scheduleRequest struct {
	Name     *string `json:"name,omitempty"`
	Project  *string `json:"project,omitempty"`
	Prompt   *string `json:"prompt,omitempty"`
	Backend  *string `json:"backend,omitempty"`
	Schedule *string `json:"schedule,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	// Schedules carry full prompt content, which can encode operational
	// instructions or sensitive context the master operator may not want
	// exposed to per-device tokens. Mutating endpoints already require
	// master; making the read master-only too keeps the surface symmetric
	// and matches Settings → projects/backends, which device tokens
	// (mobile clients) cannot edit but also do not need to enumerate.
	if requireMaster(w, r) {
		return
	}
	schedules, err := s.db.Schedules()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load schedules: " + err.Error()})
		return
	}
	out := make([]scheduleJSON, 0, len(schedules))
	for _, sc := range schedules {
		out = append(out, toScheduleJSON(sc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes)
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	sched := &model.ScheduledTask{
		Enabled: true, // default new schedules to enabled
	}
	applyScheduleRequest(sched, req)
	if err := sched.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.db.AddSchedule(sched); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[schedules] created %s (%s) schedule=%q project=%q enabled=%v", sched.ID, sched.Name, sched.Schedule, sched.Project, sched.Enabled)
	writeJSON(w, http.StatusCreated, toScheduleJSON(sched))
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes)
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	sched, err := s.db.GetSchedule(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "schedule not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	applyScheduleRequest(sched, req)
	if err := sched.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Schedule != nil {
		// Schedule expression changed — recompute next-run. Anchor on
		// LastRunAt when the schedule has fired before (so an unchanged
		// cadence preserves alignment with prior fires); otherwise anchor
		// on now. `cron.Schedule.Next(time.Time{})` returns a year-0001
		// date, which the scheduler tick would read as "due now" and fire
		// on the very next tick — violating the "no first-tick fire"
		// invariant.
		anchor := sched.LastRunAt
		if anchor.IsZero() {
			anchor = time.Now()
		}
		sched.NextRunAt = sched.NextFire(anchor)
	}
	// Clear LastError unconditionally: Validate above passed, and none of
	// the user-editable fields (name/project/backend/prompt/schedule/
	// enabled) affect a previously-stored error's relevance — any stored
	// parse error is stale by definition once Validate passes here.
	sched.LastError = ""
	if err := s.db.UpdateSchedule(sched); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[schedules] updated %s (%s) schedule=%q enabled=%v", sched.ID, sched.Name, sched.Schedule, sched.Enabled)
	writeJSON(w, http.StatusOK, toScheduleJSON(sched))
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	if err := s.db.DeleteSchedule(id); err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "schedule not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[schedules] deleted %s", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	if s.scheduler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduler not running"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	task, err := s.scheduler.RunNow(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "schedule not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uxlog.Log("[schedules] run-now %s -> task %s", id, task.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": task.ID})
}

// applyScheduleRequest copies non-nil request fields onto the schedule
// in-place. Used by both create (where every field starts zero) and update
// (where unset fields stay as-is).
func applyScheduleRequest(sched *model.ScheduledTask, req scheduleRequest) {
	if req.Name != nil {
		sched.Name = strings.TrimSpace(*req.Name)
	}
	if req.Project != nil {
		sched.Project = strings.TrimSpace(*req.Project)
	}
	if req.Prompt != nil {
		sched.Prompt = *req.Prompt
	}
	if req.Backend != nil {
		sched.Backend = strings.TrimSpace(*req.Backend)
	}
	if req.Schedule != nil {
		sched.Schedule = strings.TrimSpace(*req.Schedule)
	}
	if req.Enabled != nil {
		sched.Enabled = *req.Enabled
	}
}
