package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/orch"
)

// haltSessionNotFound is the predicate passed to orch.HaltDownstream so it
// knows which Stopper errors mean "session already exited" (a benign race
// with the depswatcher) vs a real halt failure. Centralised here so both
// the HTTP handler and any future caller in this package use the same
// definition.
func haltSessionNotFound(err error) bool { return errors.Is(err, agent.ErrSessionNotFound) }

// handleGetDeps returns the one-hop upstream + downstream of a task. Open
// to device tokens (read-only) because the DAG view runs on mobile.
func (s *Server) handleGetDeps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, err := orch.Deps(s.db, id)
	if err != nil {
		s.writeOrchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleLinkTask attaches a parent task to a child via depends_on. Body shape:
//
//	{"parent_id": "<id>"}
//
// Cycle attempts return 409 with {"cycle": [...], "error": "cycle detected: ..."}
// so the UI can render the offending path inline.
//
// Per-task scope — no requireMaster gate. Same tier as archive/rename, which
// must be reachable from device tokens for the mobile UI.
func (s *Server) handleLinkTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ParentID string `json:"parent_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := orch.Link(s.db, id, req.ParentID); err != nil {
		var ce *orch.CycleError
		if errors.As(err, &ce) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": ce.Error(),
				"cycle": ce.Path,
			})
			return
		}
		s.writeOrchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "linked"})
}

// handleUnlinkTask removes a parent from a child's depends_on. Parent ID is
// in the path (DELETE /api/tasks/{id}/deps/{parent_id}) so the call works
// without a body for browser fetch + idempotency. Per-task scope — no
// requireMaster gate.
func (s *Server) handleUnlinkTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	parentID := r.PathValue("parent_id")
	if err := orch.Unlink(s.db, id, parentID); err != nil {
		s.writeOrchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// handleHaltDownstream cascades stop/archive through TaskID's transitive
// descendants. Returns the per-row summary so the UI can render "halted N
// tasks (2 stopped, 3 archived)".
//
// requireMaster — cross-task mutation tier (matches handleStopAll). A halt
// can stop multiple agents and archive multiple rows in one call, so it
// must not be reachable from a device token that compromised on one phone.
func (s *Server) handleHaltDownstream(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id := r.PathValue("id")
	report, err := orch.HaltDownstream(s.db, s.runner, id, haltSessionNotFound)
	if err != nil {
		s.writeOrchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleSetPlanSlug stamps a task with the orchestrator grouping label.
// Body: {"plan_slug": "<slug>"}. Empty string clears it. Per-task scope —
// no requireMaster gate.
func (s *Server) handleSetPlanSlug(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		PlanSlug string `json:"plan_slug"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := orch.SetPlanSlug(s.db, id, req.PlanSlug); err != nil {
		s.writeOrchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"plan_slug": req.PlanSlug})
}

// handleDAG returns the DAG snapshot for rendering. Filters via query
// parameters:
//
//	?project=<name>     scope to a single project
//	?plan=<slug>        scope to a single orchestrator stack
//	?archived=1         include archived rows (greyed-out in the UI)
//
// All filters are optional; with none the response is every non-archived
// task in the daemon.
func (s *Server) handleDAG(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := orch.DAGFilter{
		Project:         q.Get("project"),
		PlanSlug:        q.Get("plan"),
		IncludeArchived: q.Get("archived") == "1" || strings.EqualFold(q.Get("archived"), "true"),
	}
	nodes, err := orch.ListDAG(s.db, filter)
	if err != nil {
		s.writeOrchError(w, err)
		return
	}
	if nodes == nil {
		nodes = []orch.DAGNode{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// writeOrchError maps orch / db sentinel errors to appropriate HTTP statuses.
// Anything not recognised falls through to 500 so unexpected DB failures
// surface in logs rather than masquerading as 4xx. Uses errors.Is against
// the db.ErrTaskNotFound sentinel — string-matching the error message would
// silently break on any future rename of the wrapped format.
func (s *Server) writeOrchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orch.ErrEmptyID):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, db.ErrTaskNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}
