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
	RunOnceAt  string `json:"run_once_at,omitempty"`
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
	if !s.RunOnceAt.IsZero() {
		js.RunOnceAt = s.RunOnceAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return js
}

// scheduleRequest is the wire shape for create/update bodies. All fields are
// pointers on update so the SPA can do partial updates (e.g. toggle enabled
// without resending the prompt). RunOnceAt is RFC3339 UTC; pass empty string
// to clear it when switching from one-shot back to recurring.
type scheduleRequest struct {
	Name      *string `json:"name,omitempty"`
	Project   *string `json:"project,omitempty"`
	Prompt    *string `json:"prompt,omitempty"`
	Backend   *string `json:"backend,omitempty"`
	Schedule  *string `json:"schedule,omitempty"`
	RunOnceAt *string `json:"run_once_at,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	// Single-tier auth: any authenticated token may list/manage schedules.
	// Schedules carry prompt content but no credentials, and creating one
	// cannot inject a command the way a backend template can (the denylist
	// covers backends CRUD, self-update, and token mint/revoke).
	schedules, err := s.db.Schedules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load schedules", err)
		return
	}
	out := make([]scheduleJSON, 0, len(schedules))
	for _, sc := range schedules {
		out = append(out, toScheduleJSON(sc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes)
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	sched := &model.ScheduledTask{
		Enabled: true, // default new schedules to enabled
	}
	if err := applyScheduleRequest(sched, req); err != nil {
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}
	if err := sched.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}
	// Pre-populate NextRunAt so the UI shows it before the first tick lands.
	sched.NextRunAt = sched.NextFire(time.Now())
	if err := s.db.AddSchedule(sched); err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	uxlog.Log("[schedules] created %s (%s) schedule=%q run_once_at=%s project=%q enabled=%v", sched.ID, sched.Name, sched.Schedule, formatRunOnce(sched.RunOnceAt), sched.Project, sched.Enabled)
	writeJSON(w, http.StatusCreated, toScheduleJSON(sched))
}

// formatRunOnce returns a stable string for log lines: RFC3339 UTC when set,
// "-" otherwise. Keeps log greps simple.
func formatRunOnce(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes)
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	sched, err := s.db.GetSchedule(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeErr(w, http.StatusNotFound, "schedule not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	if err := applyScheduleRequest(sched, req); err != nil {
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}
	if err := sched.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}
	if req.Schedule != nil || req.RunOnceAt != nil {
		// Cadence changed — recompute next-run. Anchor on LastRunAt when the
		// schedule has fired before (so an unchanged cadence preserves
		// alignment with prior fires); otherwise anchor on now.
		// `cron.Schedule.Next(time.Time{})` returns a year-0001 date, which
		// the scheduler tick would read as "due now" and fire on the very
		// next tick — violating the "no first-tick fire" invariant.
		// NextFire returns RunOnceAt directly for one-shots.
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
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	uxlog.Log("[schedules] updated %s (%s) schedule=%q enabled=%v", sched.ID, sched.Name, sched.Schedule, sched.Enabled)
	writeJSON(w, http.StatusOK, toScheduleJSON(sched))
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required", nil)
		return
	}
	if err := s.db.DeleteSchedule(id); err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeErr(w, http.StatusNotFound, "schedule not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	uxlog.Log("[schedules] deleted %s", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		writeErr(w, http.StatusServiceUnavailable, "scheduler not running", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required", nil)
		return
	}
	task, err := s.scheduler.RunNow(id)
	if err != nil {
		if errors.Is(err, db.ErrScheduleNotFound) {
			writeErr(w, http.StatusNotFound, "schedule not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	uxlog.Log("[schedules] run-now %s -> task %s", id, task.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": task.ID})
}

// applyScheduleRequest copies non-nil request fields onto the schedule
// in-place. Used by both create (where every field starts zero) and update
// (where unset fields stay as-is). Returns an error when run_once_at is
// malformed/past, or when the caller passes both schedule and run_once_at
// non-empty in a single request — exactly one cadence per call. When only
// one is set, the OTHER cadence anchor is cleared automatically so the
// caller doesn't have to know whether the row was previously recurring or
// one-shot. Any future run_once_at is accepted; the next fire happens at
// most one tick interval later, regardless of how close to now.
func applyScheduleRequest(sched *model.ScheduledTask, req scheduleRequest) error {
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
	// Reject ambiguous "both cadences in one call" up front. Validate would
	// not catch this because the per-field clear logic below silently picks
	// a winner.
	bothSet := req.Schedule != nil && strings.TrimSpace(*req.Schedule) != "" &&
		req.RunOnceAt != nil && strings.TrimSpace(*req.RunOnceAt) != ""
	if bothSet {
		return errors.New("specify either schedule (cron) or run_once_at, not both")
	}
	if req.Schedule != nil {
		sched.Schedule = strings.TrimSpace(*req.Schedule)
		if sched.Schedule != "" {
			sched.RunOnceAt = time.Time{}
		}
	}
	if req.RunOnceAt != nil {
		raw := strings.TrimSpace(*req.RunOnceAt)
		var newAt time.Time
		if raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return errors.New("run_once_at must be RFC3339 (e.g. 2026-05-17T14:00:00Z): " + err.Error())
			}
			if !t.After(time.Now()) {
				return errors.New("run_once_at must be in the future")
			}
			newAt = t
		}
		sched.RunOnceAt = newAt
		if !newAt.IsZero() {
			sched.Schedule = ""
		}
	}
	if req.Enabled != nil {
		sched.Enabled = *req.Enabled
	}
	return nil
}
