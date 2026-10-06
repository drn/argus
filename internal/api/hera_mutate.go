package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/uxlog"
)

// Hera mutation endpoints (openspec add-web-hera-mutations): thin adapters over
// the same db verbs / internal/hera primitives the TUI rail drives. Every
// handler acts on an explicit orchestrator or role id and NEVER hard-deletes a
// hera row (nuke stamps nuked_at).

func (s *Server) buildHeraModel() (*heramodel.Model, error) {
	runningSet, idleSet, needsInputSet := s.sessionStateMaps()
	m, err := heramodel.BuildModel(s.db, needsInputSet, idleSet, runningSet, nil)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func heraPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return 0, false
	}
	return id, true
}

// heraErr maps store errors to HTTP statuses.
func heraErr(w http.ResponseWriter, msg string, err error) {
	switch {
	case errors.Is(err, db.ErrHeraNotFound):
		writeErr(w, http.StatusNotFound, msg, err)
	case errors.Is(err, db.ErrHeraNameConflict):
		writeErr(w, http.StatusConflict, msg, err)
	default:
		writeErr(w, http.StatusInternalServerError, msg, err)
	}
}

func heraOK(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }

// heraTarget identifies the orchestrator-or-role a generic verb acts on.
type heraTarget struct {
	orch bool
	id   int64
}

func (s *Server) heraKindHandler(orch bool, fn func(w http.ResponseWriter, r *http.Request, t heraTarget)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := heraPathID(w, r)
		if !ok {
			return
		}
		fn(w, r, heraTarget{orch: orch, id: id})
	}
}

func (s *Server) handleHeraArchive(archive bool) func(http.ResponseWriter, *http.Request, heraTarget) {
	return func(w http.ResponseWriter, r *http.Request, t heraTarget) {
		var err error
		stopTask := ""
		switch {
		case t.orch && archive:
			err = s.db.ArchiveHeraOrchestrator(t.id)
		case t.orch:
			err = s.db.UnarchiveHeraOrchestrator(t.id)
		case archive:
			role, rErr := s.db.HeraRole(t.id)
			if rErr != nil {
				heraErr(w, "hide role", rErr)
				return
			}
			if err = s.db.ArchiveHeraRole(t.id); err == nil && role.Kind == db.HeraKindWorker {
				if b, bErr := s.db.HeraLiveBindingByRole(t.id); bErr == nil && b != nil {
					stopTask = b.ArgusTaskID
				}
			}
		default:
			err = s.db.UnarchiveHeraRole(t.id)
		}
		if err != nil {
			heraErr(w, "archive", err)
			return
		}
		if stopTask != "" && s.runner != nil && s.runner.HasSession(stopTask) {
			go func() {
				if sErr := s.runner.Stop(stopTask); sErr != nil {
					uxlog.Log("[api-hera] hide: stop session failed task=%s: %v", stopTask, sErr)
				}
			}()
		}
		uxlog.Log("[api-hera] archive=%v orch=%v id=%d", archive, t.orch, t.id)
		heraOK(w)
	}
}

func (s *Server) handleHeraPin(pin bool) func(http.ResponseWriter, *http.Request, heraTarget) {
	return func(w http.ResponseWriter, r *http.Request, t heraTarget) {
		var err error
		switch {
		case t.orch && pin:
			err = s.db.PinHeraOrchestrator(t.id)
		case t.orch:
			err = s.db.UnpinHeraOrchestrator(t.id)
		case pin:
			err = s.db.PinHeraRole(t.id)
		default:
			err = s.db.UnpinHeraRole(t.id)
		}
		if err != nil {
			heraErr(w, "pin", err)
			return
		}
		uxlog.Log("[api-hera] pin=%v orch=%v id=%d", pin, t.orch, t.id)
		heraOK(w)
	}
}

func (s *Server) handleHeraRename(w http.ResponseWriter, r *http.Request, t heraTarget) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	name := strings.TrimSpace(body.Name)
	var err error
	if t.orch {
		err = s.db.RenameHeraOrchestrator(t.id, name)
	} else {
		err = s.db.RenameHeraRole(t.id, name)
	}
	if err != nil {
		heraErr(w, "rename", err)
		return
	}
	uxlog.Log("[api-hera] rename orch=%v id=%d → %q", t.orch, t.id, name)
	heraOK(w)
}

func (s *Server) handleHeraRoleStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	st := db.HeraRoleStatusValue(body.Status)
	switch st {
	case db.HeraStatusIdle, db.HeraStatusWorking, db.HeraStatusBlocked, db.HeraStatusDone:
	default:
		writeErr(w, http.StatusBadRequest, "status must be idle|working|blocked|done", nil)
		return
	}
	role, err := s.db.HeraRole(id)
	if err != nil {
		heraErr(w, "role status", err)
		return
	}
	if err := s.db.UpsertHeraRoleStatus(id, st); err != nil {
		heraErr(w, "role status", err)
		return
	}
	if role.Kind == db.HeraKindWorker {
		if b, bErr := s.db.HeraLiveBindingByRole(id); bErr == nil && b != nil && b.ArgusTaskID != "" {
			if st == db.HeraStatusDone {
				if _, rErr := s.db.RollHeraWorkerToReview(b.ArgusTaskID); rErr != nil {
					uxlog.Log("[api-hera] status(done): roll failed task=%s: %v", b.ArgusTaskID, rErr)
				}
			} else if cErr := s.db.ClearHeraReadyToClose(b.ArgusTaskID); cErr != nil {
				uxlog.Log("[api-hera] status: clear ready_to_close failed task=%s: %v", b.ArgusTaskID, cErr)
			}
		}
	}
	uxlog.Log("[api-hera] role %d status → %s", id, st)
	heraOK(w)
}

func (s *Server) handleHeraKanban(w http.ResponseWriter, r *http.Request) {
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ks := db.HeraKanbanStatus(body.Status)
	switch ks {
	case db.HeraKanbanActive, db.HeraKanbanBacklog, db.HeraKanbanBlocked, db.HeraKanbanDone:
	default:
		writeErr(w, http.StatusBadRequest, "status must be active|backlog|blocked|done", nil)
		return
	}
	m, err := s.buildHeraModel()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build hera model", err)
		return
	}
	if m.OrchByID(id) == nil {
		writeErr(w, http.StatusNotFound, "orchestrator not found", nil)
		return
	}
	if _, nested := m.CanonicalParents()[id]; nested {
		writeErr(w, http.StatusBadRequest, "kanban applies to top-level orchestrators only", nil)
		return
	}
	if err := s.db.SetHeraOrchestratorKanbanStatus(id, ks); err != nil {
		heraErr(w, "kanban", err)
		return
	}
	uxlog.Log("[api-hera] orch %d kanban → %s", id, ks)
	heraOK(w)
}

func (s *Server) handleHeraNukePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	m, err := s.buildHeraModel()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build hera model", err)
		return
	}
	subtree := m.BridgeSubtree(id)
	if len(subtree) == 0 {
		writeErr(w, http.StatusNotFound, "orchestrator not found", nil)
		return
	}
	p := hera.PreviewNukeSubtree(s.db, subtree)
	writeJSON(w, http.StatusOK, map[string]any{"name": subtree[0].Name, "preview": p})
}

func (s *Server) handleHeraNukeOrch(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	m, err := s.buildHeraModel()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build hera model", err)
		return
	}
	subtree := m.BridgeSubtree(id)
	if len(subtree) == 0 {
		writeErr(w, http.StatusNotFound, "orchestrator not found", nil)
		return
	}
	p := hera.PreviewNukeSubtree(s.db, subtree)
	reclaimed, bg := hera.NukeSubtree(s.db, s.nukeRunner(), subtree)
	s.goHeraBG(bg)
	uxlog.Log("[api-hera] nuke orch %d: %d orchestrators, %d tasks reclaimed", id, len(subtree), len(reclaimed))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preview": p})
}

func (s *Server) handleHeraNukeRole(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	id, ok := heraPathID(w, r)
	if !ok {
		return
	}
	m, err := s.buildHeraModel()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build hera model", err)
		return
	}
	rv := m.RoleByID(id)
	if rv == nil {
		writeErr(w, http.StatusNotFound, "role not found", nil)
		return
	}
	bg := hera.NukeSingleRole(s.db, s.nukeRunner(), rv)
	s.goHeraBG(bg)
	uxlog.Log("[api-hera] nuke role %d", id)
	heraOK(w)
}

// nukeRunner returns the session runner as an interface value, or nil when the
// server has none (avoids a typed-nil interface).
func (s *Server) nukeRunner() hera.NukeRunner {
	if s.runner == nil {
		return nil
	}
	return s.runner
}

// goHeraBG runs a nuke's slow tail (session stops + reclaim sweep) off the
// request goroutine; heraBG lets tests (and shutdown) wait for it.
func (s *Server) goHeraBG(fn func()) {
	s.heraBG.Add(1)
	go func() {
		defer s.heraBG.Done()
		fn()
	}()
}
