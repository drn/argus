package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
)

// This file implements the eight Hera mutation REST endpoints
// (add-hera-mutation-rest-api): spawn worker, send message, and the five
// plan-DAG authoring verbs, all under
// /api/hera/orchestrators/{orch_id}/... . Every endpoint resolves its target
// orchestrator's LIVE COORDINATOR role server-side from {orch_id} and acts as
// that role — no client-supplied field ever identifies the sender/actor (see
// the proposal's "REST mutations act as the target orchestrator's
// coordinator" design principle). The business logic (validation, defaulting,
// store orchestration) is shared with the native hera_* MCP tools via
// internal/hera — this file owns only HTTP transport: request decoding,
// {orch_id}/{role_id} resolution, and status-code mapping.

// resolveOrchestratorCoordinator parses orchIDStr, resolves the orchestrator,
// and resolves a live coordinator role for it — the shared precondition for
// every Hera mutation endpoint. On failure it writes the appropriate error
// response (400 non-numeric id, 404 unknown orchestrator, 409 no live
// coordinator) and returns ok=false; callers must return immediately.
//
// coordProject is the coordinator's own bound task's project — the
// authoritative project-default fallback (mirrors internal/mcp's
// caller.task.Project, NOT role.ArgusProject, for the same reason: historical
// roles created before the M4 fix have an empty argus_project).
func (s *Server) resolveOrchestratorCoordinator(w http.ResponseWriter, orchIDStr string) (orch *db.HeraOrchestrator, coord *db.HeraRole, coordProject string, ok bool) {
	orchID, err := strconv.ParseInt(orchIDStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid orch_id", err)
		return nil, nil, "", false
	}
	o, err := s.db.HeraOrchestrator(orchID)
	if err != nil {
		if errors.Is(err, db.ErrHeraNotFound) {
			writeErr(w, http.StatusNotFound, "orchestrator not found", nil)
		} else {
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return nil, nil, "", false
	}
	coords, err := s.db.ListHeraRolesByKind(o.ID, db.HeraKindCoordinator)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return nil, nil, "", false
	}
	for _, c := range coords {
		binding, bErr := s.db.HeraLiveBindingByRole(c.ID)
		if bErr != nil {
			continue
		}
		task, tErr := s.db.Get(binding.ArgusTaskID)
		if tErr != nil {
			continue
		}
		return o, c, task.Project, true
	}
	writeErr(w, http.StatusConflict, "orchestrator has no live coordinator", nil)
	return nil, nil, "", false
}

// resolveOrchRoleByID resolves roleID and verifies it belongs to orchID,
// writing a 404 on either failure. Used by the role_id-addressed endpoints
// (message recipient, plan-node update/cancel).
func (s *Server) resolveOrchRoleByID(w http.ResponseWriter, orchID, roleID int64, what string) (*db.HeraRole, bool) {
	role, err := s.db.HeraRole(roleID)
	if err != nil || role.OrchestratorID != orchID {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("%s not found in this orchestrator", what), nil)
		return nil, false
	}
	return role, true
}

// isBadHeraSpawnInput classifies a hera.SpawnWorker error as a client error
// (unknown backend) vs a genuine server failure. There is no sentinel error
// for an unknown backend (internal/agent.resolveBackend returns a plain
// fmt.Errorf); this string match is the pragmatic alternative to plumbing one
// through just for REST's status-code mapping.
func isBadHeraSpawnInput(err error) bool {
	return strings.Contains(err.Error(), "not found in config")
}

// heraPlanErrStatus maps a plan-DAG mutation error (from internal/hera or the
// internal/db block sentinels it passes through) to an HTTP status. Shared by
// the whole-graph /plan endpoint and the standalone /plan/blocks endpoint,
// since both ultimately call the same AddHeraBlock/CreateHeraPlan validation.
func heraPlanErrStatus(err error) int {
	switch {
	case errors.Is(err, db.ErrHeraBlockCycle):
		return http.StatusConflict
	case errors.Is(err, db.ErrHeraBlockCrossOrchestrator),
		errors.Is(err, db.ErrHeraBlockSelf),
		errors.Is(err, db.ErrHeraBlockCoordinator):
		return http.StatusBadRequest
	case errors.Is(err, db.ErrHeraNotFound):
		return http.StatusNotFound
	default:
		// Everything else reaching here is hera.CreatePlan/CreatePlanNode's own
		// input validation (missing name/prompt/goal, unresolvable edge name,
		// no project resolved) — all client errors.
		return http.StatusBadRequest
	}
}

// handleHeraSpawnWorker implements POST /api/hera/orchestrators/{orch_id}/workers.
func (s *Server) handleHeraSpawnWorker(w http.ResponseWriter, r *http.Request) {
	orch, coord, coordProject, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	if s.heraSpawn == nil {
		writeErr(w, http.StatusServiceUnavailable, "hera spawn not configured", nil)
		return
	}
	var req struct {
		Prompt   string `json:"prompt"`
		RoleName string `json:"role_name"`
		Project  string `json:"project"`
		Branch   string `json:"branch"`
		Backend  string `json:"backend"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}

	res, project, err := hera.SpawnWorker(s.heraSpawn, hera.SpawnWorkerParams{
		OrchID:             orch.ID,
		OrchName:           orch.Name,
		CoordinatorName:    coord.Name,
		CoordinatorProject: coordProject,
		RoleName:           req.RoleName,
		Prompt:             req.Prompt,
		Project:            req.Project,
		Branch:             req.Branch,
		Backend:            req.Backend,
		Model:              req.Model,
	})
	if err != nil {
		switch {
		case errors.Is(err, hera.ErrPromptRequired), errors.Is(err, hera.ErrNoProject):
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
		case isBadHeraSpawnInput(err):
			writeErr(w, http.StatusBadRequest, "spawn worker failed", err)
		default:
			writeErr(w, http.StatusInternalServerError, "spawn worker failed", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"role_id":       res.Role.ID,
		"orch_id":       orch.ID,
		"name":          res.Role.Name,
		"kind":          string(res.Role.Kind),
		"project":       project,
		"argus_task_id": res.Task.ID,
		"task_name":     res.Task.Name,
		"task_status":   res.Task.Status.String(),
	})
}

// handleHeraSendMessage implements POST /api/hera/orchestrators/{orch_id}/messages.
// Coordinator-as-sender only — no from_role_id, no status (see the proposal's
// "Send-endpoint generality" resolved open question).
func (s *Server) handleHeraSendMessage(w http.ResponseWriter, r *http.Request) {
	orch, coord, _, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	if s.heraSvc == nil {
		writeErr(w, http.StatusServiceUnavailable, "hera messaging not configured", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, int64(model.MaxMessageBodyBytes)+4*1024)
	var req struct {
		To        int64  `json:"to"`
		Tldr      string `json:"tldr"`
		Body      string `json:"body"`
		InReplyTo *int64 `json:"in_reply_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if req.To == 0 || strings.TrimSpace(req.Tldr) == "" || req.Body == "" {
		writeErr(w, http.StatusBadRequest, "to, tldr, and body are required", nil)
		return
	}
	if len(req.Tldr) > db.HeraMaxTldrLen {
		writeErr(w, http.StatusBadRequest, "tldr exceeds 120 characters", nil)
		return
	}

	toRole, ok := s.resolveOrchRoleByID(w, orch.ID, req.To, "recipient role")
	if !ok {
		return
	}

	msg, err := s.heraSvc.Send(coord.ID, toRole.ID, req.Body, req.Tldr, req.InReplyTo)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrHeraMessageSelfSend):
			writeErr(w, http.StatusConflict, "cannot send a message to self", nil)
		case errors.Is(err, db.ErrHeraMessageBodyTooLarge):
			writeErr(w, http.StatusRequestEntityTooLarge, "body exceeds 64 KiB", nil)
		case errors.Is(err, db.ErrHeraMessageInboxFull):
			writeErr(w, http.StatusTooManyRequests, "recipient inbox full (500 unread cap)", nil)
		case errors.Is(err, db.ErrHeraMessageRateLimited):
			writeErr(w, http.StatusTooManyRequests, "sender rate limit exceeded (50/min)", nil)
		case errors.Is(err, db.ErrHeraMessageTldrRequired),
			errors.Is(err, db.ErrHeraMessageTldrTooLong),
			errors.Is(err, db.ErrHeraMessageTldrMultiline):
			writeErr(w, http.StatusBadRequest, "", err)
		case errors.Is(err, db.ErrHeraMessageRecipientInvalid):
			writeErr(w, http.StatusNotFound, "recipient role is missing or archived", nil)
		default:
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message_id":    msg.ID,
		"to_role_id":    toRole.ID,
		"delivery_mode": msg.DeliveryMode,
	})
}

// handleHeraPlanNodeCreate implements POST /api/hera/orchestrators/{orch_id}/plan/nodes.
func (s *Server) handleHeraPlanNodeCreate(w http.ResponseWriter, r *http.Request) {
	orch, _, coordProject, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Prompt  string `json:"prompt"`
		Goal    string `json:"goal"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required", nil)
		return
	}
	nodeKind, prompt, err := hera.ResolvePlanNodeKind(req.Kind, req.Prompt, req.Goal)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	role, err := hera.CreatePlanNode(s.db, orch.ID, coordProject, name, nodeKind, prompt, req.Project)
	if err != nil {
		switch {
		case errors.Is(err, hera.ErrNoProject):
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
		default:
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"role_id": role.ID,
		"name":    role.Name,
		"project": role.ArgusProject,
		"kind":    string(role.NodeKind),
		"status":  "planned",
	})
}

// handleHeraPlanCreate implements POST /api/hera/orchestrators/{orch_id}/plan
// — the whole-graph endpoint. Edge endpoints reference NAMES (an in-batch
// node's name or an existing role's current name), matching hera_plan exactly
// — in-batch nodes have no id until the transaction commits.
func (s *Server) handleHeraPlanCreate(w http.ResponseWriter, r *http.Request) {
	orch, _, coordProject, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	var req struct {
		Nodes []struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Prompt  string `json:"prompt"`
			Goal    string `json:"goal"`
			Project string `json:"project"`
		} `json:"nodes"`
		Edges []struct {
			Blocked string `json:"blocked"`
			Blocker string `json:"blocker"`
		} `json:"edges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if len(req.Nodes) == 0 {
		writeErr(w, http.StatusBadRequest, "nodes is required (non-empty list)", nil)
		return
	}

	nodes := make([]hera.PlanNodeSpec, len(req.Nodes))
	for i, n := range req.Nodes {
		nodes[i] = hera.PlanNodeSpec{Name: n.Name, Kind: n.Kind, Prompt: n.Prompt, Goal: n.Goal, Project: n.Project}
	}
	edges := make([]hera.PlanEdgeSpec, len(req.Edges))
	for i, e := range req.Edges {
		edges[i] = hera.PlanEdgeSpec{Blocked: e.Blocked, Blocker: e.Blocker}
	}
	resolveExisting := func(name string) (*db.HeraRole, error) {
		role, err := s.db.HeraRoleByName(orch.ID, name)
		if errors.Is(err, db.ErrHeraNotFound) {
			return nil, fmt.Errorf("role %q not found in this plan or orchestrator %q", name, orch.Name)
		}
		return role, err
	}

	created, err := hera.CreatePlan(s.db, orch.ID, coordProject, nodes, edges, resolveExisting)
	if err != nil {
		writeErr(w, heraPlanErrStatus(err), err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"nodes_created": len(created),
		"edges_created": len(req.Edges),
	})
}

// handleHeraPlanNodeUpdate implements
// PATCH /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}.
func (s *Server) handleHeraPlanNodeUpdate(w http.ResponseWriter, r *http.Request) {
	orch, _, _, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	roleID, err := strconv.ParseInt(r.PathValue("role_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid role_id", err)
		return
	}
	role, ok := s.resolveOrchRoleByID(w, orch.ID, roleID, "role")
	if !ok {
		return
	}

	var req struct {
		Prompt  string `json:"prompt"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}

	if err := hera.UpdatePlanNode(s.db, role.ID, req.Prompt, req.Project); err != nil {
		switch {
		case errors.Is(err, hera.ErrEmptyPlanUpdate):
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
		case errors.Is(err, hera.ErrAlreadyMaterialized):
			writeErr(w, http.StatusConflict, err.Error(), nil)
		default:
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"role_id": role.ID, "status": "planned"})
}

// handleHeraPlanNodeCancel implements
// POST /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}/cancel.
func (s *Server) handleHeraPlanNodeCancel(w http.ResponseWriter, r *http.Request) {
	orch, _, _, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	roleID, err := strconv.ParseInt(r.PathValue("role_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid role_id", err)
		return
	}
	role, ok := s.resolveOrchRoleByID(w, orch.ID, roleID, "role")
	if !ok {
		return
	}

	if err := hera.CancelPlanNode(s.db, role.ID); err != nil {
		switch {
		case errors.Is(err, hera.ErrAlreadyMaterialized):
			writeErr(w, http.StatusConflict, err.Error(), nil)
		default:
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"role_id": role.ID, "status": "cancelled"})
}

// handleHeraBlockCreate implements
// POST /api/hera/orchestrators/{orch_id}/plan/blocks.
func (s *Server) handleHeraBlockCreate(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	var req struct {
		BlockedRoleID int64 `json:"blocked_role_id"`
		BlockerRoleID int64 `json:"blocker_role_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if req.BlockedRoleID == 0 || req.BlockerRoleID == 0 {
		writeErr(w, http.StatusBadRequest, "blocked_role_id and blocker_role_id are both required", nil)
		return
	}

	if err := hera.AddBlock(s.db, req.BlockedRoleID, req.BlockerRoleID); err != nil {
		writeErr(w, heraPlanErrStatus(err), err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"blocked_role_id": req.BlockedRoleID,
		"blocker_role_id": req.BlockerRoleID,
	})
}

// handleHeraBlockDelete implements
// DELETE /api/hera/orchestrators/{orch_id}/plan/blocks. Idempotent: removing a
// non-existent edge succeeds (mirrors hera_unblock).
func (s *Server) handleHeraBlockDelete(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := s.resolveOrchestratorCoordinator(w, r.PathValue("orch_id"))
	if !ok {
		return
	}
	blockedID, err1 := strconv.ParseInt(r.URL.Query().Get("blocked_role_id"), 10, 64)
	blockerID, err2 := strconv.ParseInt(r.URL.Query().Get("blocker_role_id"), 10, 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "blocked_role_id and blocker_role_id query params are both required", nil)
		return
	}

	if err := hera.RemoveBlock(s.db, blockedID, blockerID); err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"blocked_role_id": blockedID,
		"blocker_role_id": blockerID,
	})
}
