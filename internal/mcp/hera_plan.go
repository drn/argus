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

	// Validate and build node specs, mapping the planner's SUPPLIED name → batch
	// index so edges can reference in-batch nodes (a duplicate name within one
	// plan is the planner's bug; the last one wins for resolution). The names are
	// uniquified within the orchestrator up front; the actual node + edge inserts
	// all happen in ONE store transaction (CreateHeraPlan) so any error rolls the
	// whole graph back — nothing partial survives.
	specs := make([]db.HeraPlannedNodeSpec, 0, len(p.Nodes))
	nameIdx := map[string]int{}
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
		specs = append(specs, db.HeraPlannedNodeSpec{Name: uniqueName, ArgusProject: project, Prompt: prompt})
		nameIdx[name] = i
	}

	// Resolve edge endpoints to either an in-batch node index or a pre-existing
	// orchestrator role id (so an edge can reference a pre-existing planned/live
	// role). This resolution is name → reference only — the actual insert + cycle
	// check happens inside CreateHeraPlan's transaction.
	edgeSpecs := make([]db.HeraBlockSpec, 0, len(p.Edges))
	for i, e := range p.Edges {
		blockedIdx, blockedID, errResp := s.resolvePlanEndpoint(id, caller, nameIdx, e.Blocked, fmt.Sprintf("edges[%d].blocked", i))
		if errResp != nil {
			return errResp
		}
		blockerIdx, blockerID, errResp := s.resolvePlanEndpoint(id, caller, nameIdx, e.Blocker, fmt.Sprintf("edges[%d].blocker", i))
		if errResp != nil {
			return errResp
		}
		edgeSpecs = append(edgeSpecs, db.HeraBlockSpec{
			BlockedNodeIdx: blockedIdx, BlockedRoleID: blockedID,
			BlockerNodeIdx: blockerIdx, BlockerRoleID: blockerID,
		})
	}

	created, err := s.heraStore.CreateHeraPlan(caller.orch.ID, specs, edgeSpecs)
	if err != nil {
		return toolError(id, fmt.Sprintf("submit plan: %s", heraBlockErrMessage(err)))
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

// resolvePlanEndpoint resolves an edge endpoint name within a hera_plan call to
// either an in-batch node (returning its batch index, roleID 0) or a pre-existing
// orchestrator role (returning index -1 and the role id). In-batch names take
// precedence so an edge prefers a node created in this same plan. Returns a tool
// error response when the name is empty or resolves to neither.
func (s *Server) resolvePlanEndpoint(id interface{}, caller *callerRoleResult, nameIdx map[string]int, name, field string) (int, int64, *Response) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, 0, toolError(id, fmt.Sprintf("%s: name is required", field))
	}
	if idx, ok := nameIdx[trimmed]; ok {
		return idx, 0, nil
	}
	role, err := s.heraStore.HeraRoleByName(caller.orch.ID, trimmed)
	if errors.Is(err, db.ErrHeraNotFound) {
		return 0, 0, toolError(id, fmt.Sprintf("%s: role %q not found in this plan or orchestrator %q", field, name, caller.orch.Name))
	}
	if err != nil {
		return 0, 0, toolError(id, fmt.Sprintf("%s: resolve role %q: %v", field, name, err))
	}
	return -1, role.ID, nil
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
