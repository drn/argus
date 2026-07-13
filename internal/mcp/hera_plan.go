package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
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
		Goal         string `json:"goal"`
		Kind         string `json:"kind"`
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

	// Kind/prompt/goal resolution is pure and caller-identity-agnostic — shared
	// with hera_plan's per-node validation and the REST plan-node endpoint via
	// internal/hera.
	nodeKind, prompt, kindErr := hera.ResolvePlanNodeKind(p.Kind, p.Prompt, p.Goal)
	if kindErr != nil {
		switch {
		case errors.Is(kindErr, hera.ErrGoalRequired):
			return toolError(id, "subcoord node requires a goal (goal is the delivery prompt for the spawned coordinator)")
		case errors.Is(kindErr, hera.ErrPromptRequired):
			return toolError(id, "prompt is required")
		default:
			return toolError(id, kindErr.Error())
		}
	}

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	// Project resolution, name uniquification, and the store insert are the
	// caller-identity-agnostic post-guard body — shared with internal/api's
	// REST plan-node endpoint via internal/hera.
	role, err := hera.CreatePlanNode(s.heraStore, caller.orch.ID, caller.task.Project, name, nodeKind, prompt, p.Project)
	if err != nil {
		switch {
		case errors.Is(err, hera.ErrNoProject):
			return toolError(id, "no project resolved (coordinator task has no project and none was supplied)")
		default:
			return toolError(id, err.Error())
		}
	}

	slog.Info("[hera] plan_node ok", "orch", caller.orch.Name, "role", role.Name, "role_id", role.ID, "kind", string(nodeKind))
	var b strings.Builder
	fmt.Fprintf(&b, "Planned node created.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **role_id**: %d\n", role.ID)
	fmt.Fprintf(&b, "- **project**: %s\n", role.ArgusProject)
	fmt.Fprintf(&b, "- **kind**: %s\n", string(nodeKind))
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

	if err := hera.AddBlock(s.heraStore, blockedRole.ID, blockerRole.ID); err != nil {
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
			Goal    string `json:"goal"`
			Kind    string `json:"kind"`
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

	// Node/edge validation, name uniquification, and the whole-graph store
	// transaction are the caller-identity-agnostic post-guard body — shared
	// with internal/api's REST whole-graph endpoint via internal/hera. Edge
	// endpoints resolve by NAME (an in-batch node's name, else an existing
	// role's name within the orchestrator) since in-batch nodes have no id
	// until the transaction commits.
	nodes := make([]hera.PlanNodeSpec, len(p.Nodes))
	for i, n := range p.Nodes {
		nodes[i] = hera.PlanNodeSpec{Name: n.Name, Kind: n.Kind, Prompt: n.Prompt, Goal: n.Goal, Project: n.Project}
	}
	edges := make([]hera.PlanEdgeSpec, len(p.Edges))
	for i, e := range p.Edges {
		edges[i] = hera.PlanEdgeSpec{Blocked: e.Blocked, Blocker: e.Blocker}
	}
	resolveExisting := func(name string) (*db.HeraRole, error) {
		role, err := s.heraStore.HeraRoleByName(caller.orch.ID, name)
		if errors.Is(err, db.ErrHeraNotFound) {
			return nil, fmt.Errorf("role %q not found in this plan or orchestrator %q", name, caller.orch.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve role %q: %w", name, err)
		}
		return role, nil
	}

	created, err := hera.CreatePlan(s.heraStore, caller.orch.ID, caller.task.Project, nodes, edges, resolveExisting)
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

func (s *Server) toolHeraPlanNodeUpdate(id interface{}, args json.RawMessage) *Response {
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
	project := strings.TrimSpace(p.Project)
	if prompt == "" && project == "" {
		return toolError(id, "at least one of prompt or project is required")
	}

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	role, errResp := s.resolveOrchRole(id, caller.orch.ID, caller.orch.Name, name)
	if errResp != nil {
		return errResp
	}

	if err := hera.UpdatePlanNode(s.heraStore, role.ID, prompt, project); err != nil {
		switch {
		case errors.Is(err, hera.ErrAlreadyMaterialized):
			return toolError(id, fmt.Sprintf("role %q has already materialized (prompt already delivered); cannot update a running worker via the plan", name))
		case errors.Is(err, hera.ErrEmptyPlanUpdate):
			return toolError(id, "at least one of prompt or project is required")
		default:
			return toolError(id, fmt.Sprintf("update planned node: %v", err))
		}
	}

	slog.Info("[hera plan] plan_node_update ok", "orch", caller.orch.Name, "role", role.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Planned node updated.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **status**: planned (not yet materialized)\n")
	return toolResult(id, b.String())
}

func (s *Server) toolHeraUnblock(id interface{}, args json.RawMessage) *Response {
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

	if err := hera.RemoveBlock(s.heraStore, blockedRole.ID, blockerRole.ID); err != nil {
		return toolError(id, fmt.Sprintf("remove block: %v", err))
	}

	slog.Info("[hera plan] unblock ok", "orch", caller.orch.Name, "blocked", blockedRole.Name, "blocker", blockerRole.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Blocking edge removed (idempotent — no-op if the edge did not exist).\n\n")
	fmt.Fprintf(&b, "- **blocked**: %s\n", blockedRole.Name)
	fmt.Fprintf(&b, "- **blocker**: %s\n", blockerRole.Name)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraPlanNodeCancel(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Name         string `json:"name"`
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

	caller, errResp := s.heraPlanCoordinatorGuard(id, p.Cwd, p.Orchestrator)
	if errResp != nil {
		return errResp
	}

	role, errResp := s.resolveOrchRole(id, caller.orch.ID, caller.orch.Name, name)
	if errResp != nil {
		return errResp
	}

	if err := hera.CancelPlanNode(s.heraStore, role.ID); err != nil {
		switch {
		case errors.Is(err, hera.ErrAlreadyMaterialized):
			return toolError(id, fmt.Sprintf("role %q has already materialized; stop the running worker via the task lifecycle instead", name))
		default:
			return toolError(id, fmt.Sprintf("cancel planned node: %v", err))
		}
	}

	slog.Info("[hera plan] plan_node_cancel ok", "orch", caller.orch.Name, "role", role.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Planned node cancelled.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **status**: cancelled (kept in plan for visibility; will not materialize; no longer gates dependents)\n")
	return toolResult(id, b.String())
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
