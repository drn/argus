package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/drn/argus/internal/db"
)

// Native plan-DAG authoring tools (add-hera-plan-substrate): hera_plan_node,
// hera_block, hera_plan. All three are COORDINATOR-ONLY (mirroring
// hera_spawn_worker): a worker or freelance caller is rejected. They author the
// declarative plan; the daemon gater (internal/heragater) materializes planned
// nodes once their blockers reach role-status done.

// heraPlanCoordinatorGuard resolves the caller and ensures it is a coordinator.
// Returns (caller, nil) on success or (nil, errorResponse) when resolution fails
// or the caller is not a coordinator.
func (s *Server) heraPlanCoordinatorGuard(id interface{}, cwd, orchestrator string) (*callerRoleResult, *Response) {
	caller, err := s.resolveCallerRole(cwd, orchestrator)
	if err != nil {
		return nil, toolError(id, err.Error())
	}
	if caller.role.Kind != db.HeraKindCoordinator {
		return nil, toolError(id, fmt.Sprintf(
			"caller role %q has kind %q; only coordinators may author the plan",
			caller.role.Name, caller.role.Kind))
	}
	return caller, nil
}

func (s *Server) toolHeraPlanNode(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Name         string `json:"name"`
		Prompt       string `json:"prompt"`
		Project      string `json:"project"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return toolError(id, "name is required")
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return toolError(id, "prompt is required")
	}

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	project := strings.TrimSpace(p.Project)
	if project == "" {
		project = caller.task.Project
	}
	if project == "" {
		return toolError(id, "no project resolved (coordinator task has no project and none was supplied)")
	}

	uniqueName, err := s.heraStore.UniqueHeraRoleName(caller.orch.ID, name)
	if err != nil {
		return toolError(id, fmt.Sprintf("uniquify name: %v", err))
	}

	role, err := s.heraStore.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: caller.orch.ID,
		Name:           uniqueName,
		ArgusProject:   project,
		Prompt:         prompt,
	})
	if err != nil {
		return toolError(id, fmt.Sprintf("create planned node: %v", err))
	}

	slog.Info("[hera] plan_node ok", "orch", caller.orch.Name, "role", role.Name, "role_id", role.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "Planned node created.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **role_id**: %d\n", role.ID)
	fmt.Fprintf(&b, "- **project**: %s\n", project)
	fmt.Fprintf(&b, "- **status**: planned (no agent yet; materializes when blockers reach done)\n")
	return toolResult(id, b.String())
}

func (s *Server) toolHeraBlock(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Blocked      string `json:"blocked"`
		Blocker      string `json:"blocker"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if strings.TrimSpace(p.Blocked) == "" || strings.TrimSpace(p.Blocker) == "" {
		return toolError(id, "blocked and blocker are both required")
	}

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	blockedRole, errResp := s.resolveOrchRole(id, caller.orch.ID, caller.orch.Name, p.Blocked)
	if errResp != nil {
		return errResp
	}
	blockerRole, errResp := s.resolveOrchRole(id, caller.orch.ID, caller.orch.Name, p.Blocker)
	if errResp != nil {
		return errResp
	}

	if err := s.heraStore.AddHeraBlock(blockedRole.ID, blockerRole.ID); err != nil {
		return toolError(id, heraBlockErrMessage(err))
	}

	slog.Info("[hera] block ok", "orch", caller.orch.Name, "blocked", blockedRole.Name, "blocker", blockerRole.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Blocking edge added.\n\n")
	fmt.Fprintf(&b, "- **blocked**: %s (waits)\n", blockedRole.Name)
	fmt.Fprintf(&b, "- **blocker**: %s (must reach role-status done first)\n", blockerRole.Name)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraPlan(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd   string `json:"cwd"`
		Nodes []struct {
			Name    string `json:"name"`
			Prompt  string `json:"prompt"`
			Project string `json:"project"`
		} `json:"nodes"`
		Edges []struct {
			Blocked string `json:"blocked"`
			Blocker string `json:"blocker"`
		} `json:"edges"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if len(p.Nodes) == 0 {
		return toolError(id, "nodes is required (non-empty list)")
	}

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	// Create nodes first, recording name → role so edges can reference by name.
	// On any error after the first node is created we DO NOT roll back the rows
	// individually — the store has no multi-call tx seam exposed here — but we
	// fail fast so a partial plan is at least diagnosable. Cycle/cross-orch
	// rejection happens per-edge below, before any edge is stored, so the only
	// partial-state risk is a bad edge after good nodes; that is acceptable (the
	// nodes are valid planned rows the coordinator can edge later or delete).
	created := map[string]*db.HeraRole{}
	for i, n := range p.Nodes {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			return toolError(id, fmt.Sprintf("nodes[%d]: name is required", i))
		}
		prompt := strings.TrimSpace(n.Prompt)
		if prompt == "" {
			return toolError(id, fmt.Sprintf("nodes[%d] (%s): prompt is required", i, name))
		}
		project := strings.TrimSpace(n.Project)
		if project == "" {
			project = caller.task.Project
		}
		if project == "" {
			return toolError(id, fmt.Sprintf("nodes[%d] (%s): no project resolved", i, name))
		}
		uniqueName, err := s.heraStore.UniqueHeraRoleName(caller.orch.ID, name)
		if err != nil {
			return toolError(id, fmt.Sprintf("nodes[%d] (%s): uniquify: %v", i, name, err))
		}
		role, err := s.heraStore.CreateHeraPlannedRole(db.CreateHeraRoleInput{
			OrchestratorID: caller.orch.ID,
			Name:           uniqueName,
			ArgusProject:   project,
			Prompt:         prompt,
		})
		if err != nil {
			return toolError(id, fmt.Sprintf("nodes[%d] (%s): create: %v", i, name, err))
		}
		// Key by the SUPPLIED name (edges reference the planner's names, not the
		// uniquified form — a duplicate name within one plan is the planner's bug).
		created[name] = role
	}

	// Then edges. A node name resolves first against this plan's created nodes,
	// then against existing roles in the orchestrator (so an edge can reference a
	// pre-existing planned/live role).
	for i, e := range p.Edges {
		blocked, errResp := s.resolvePlanRole(id, caller, created, e.Blocked, fmt.Sprintf("edges[%d].blocked", i))
		if errResp != nil {
			return errResp
		}
		blocker, errResp := s.resolvePlanRole(id, caller, created, e.Blocker, fmt.Sprintf("edges[%d].blocker", i))
		if errResp != nil {
			return errResp
		}
		if err := s.heraStore.AddHeraBlock(blocked.ID, blocker.ID); err != nil {
			return toolError(id, fmt.Sprintf("edges[%d] (%s<-%s): %s", i, e.Blocked, e.Blocker, heraBlockErrMessage(err)))
		}
	}

	slog.Info("[hera] plan ok", "orch", caller.orch.Name, "nodes", len(p.Nodes), "edges", len(p.Edges))
	var b strings.Builder
	fmt.Fprintf(&b, "Plan submitted.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **nodes_created**: %d\n", len(created))
	fmt.Fprintf(&b, "- **edges_created**: %d\n", len(p.Edges))
	return toolResult(id, b.String())
}

// resolveOrchRole resolves a role by name within an orchestrator, returning a
// tool error response when it is missing.
func (s *Server) resolveOrchRole(id interface{}, orchID int64, orchName, name string) (*db.HeraRole, *Response) {
	role, err := s.heraStore.HeraRoleByName(orchID, strings.TrimSpace(name))
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, toolError(id, fmt.Sprintf("role %q not found in orchestrator %q", name, orchName))
	}
	if err != nil {
		return nil, toolError(id, fmt.Sprintf("resolve role %q: %v", name, err))
	}
	return role, nil
}

// resolvePlanRole resolves an edge endpoint name within a hera_plan call: first
// against the just-created nodes (by supplied name), then against existing
// orchestrator roles.
func (s *Server) resolvePlanRole(id interface{}, caller *callerRoleResult, created map[string]*db.HeraRole, name, field string) (*db.HeraRole, *Response) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, toolError(id, fmt.Sprintf("%s: name is required", field))
	}
	if r, ok := created[trimmed]; ok {
		return r, nil
	}
	role, err := s.heraStore.HeraRoleByName(caller.orch.ID, trimmed)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, toolError(id, fmt.Sprintf("%s: role %q not found in this plan or orchestrator %q", field, name, caller.orch.Name))
	}
	if err != nil {
		return nil, toolError(id, fmt.Sprintf("%s: resolve role %q: %v", field, name, err))
	}
	return role, nil
}

// heraBlockErrMessage maps the store's block sentinels to agent-facing messages.
func heraBlockErrMessage(err error) string {
	switch {
	case errors.Is(err, db.ErrHeraBlockCycle):
		return "blocking edge would create a cycle; not stored"
	case errors.Is(err, db.ErrHeraBlockCrossOrchestrator):
		return "blocking edge endpoints are in different orchestrators (v1 sequences sub-teams at the parent level instead)"
	case errors.Is(err, db.ErrHeraBlockSelf):
		return "a role cannot block itself"
	case errors.Is(err, db.ErrHeraNotFound):
		return "one of the edge's roles no longer exists"
	default:
		return fmt.Sprintf("add blocking edge: %v", err)
	}
}
