package hera

import (
	"errors"
	"fmt"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// This file holds the caller-identity-agnostic body of the hera mutation
// verbs (hera_spawn_worker, hera_plan_node, hera_block, hera_plan,
// hera_plan_node_update, hera_unblock, hera_plan_node_cancel) — everything
// past "resolve the target orchestrator's coordinator role". internal/mcp
// resolves that identity from a calling agent's cwd; internal/api resolves it
// from an {orch_id} path param and the orchestrator's live coordinator
// binding. Both call the functions here so the validation, defaulting, and
// store-orchestration logic can never drift between the two front ends.

// Sentinel errors shared between internal/mcp and internal/api. These guard
// mutation input shape/state, distinct from the internal/db sentinels (which
// guard store-level invariants like a block cycle or message cap).
var (
	// ErrPromptRequired fires when a worker-kind spawn/plan-node call has no
	// (non-blank) prompt.
	ErrPromptRequired = errors.New("hera mutation: prompt is required")
	// ErrGoalRequired fires when a kind=subcoord plan-node call has no
	// (non-blank) goal — the subcoord's delivery prompt.
	ErrGoalRequired = errors.New("hera mutation: subcoord node requires a goal")
	// ErrNoProject fires when neither an explicit project override nor the
	// coordinator's own task project resolves to a non-blank value.
	ErrNoProject = errors.New("hera mutation: no project resolved")
	// ErrEmptyPlanUpdate fires when a plan-node update supplies neither prompt
	// nor project.
	ErrEmptyPlanUpdate = errors.New("hera mutation: at least one of prompt or project is required")
	// ErrAlreadyMaterialized fires when a plan-node update/cancel targets a
	// role that already holds a binding (its prompt was already delivered).
	ErrAlreadyMaterialized = errors.New("hera mutation: role has already materialized")
)

// MutationStore is the store surface the plan-mutation helpers need. Overlaps
// with mcp.HeraStore's plan-DAG methods; satisfied by *db.DB.
type MutationStore interface {
	UniqueHeraRoleName(orchID int64, base string) (string, error)
	CreateHeraPlannedRole(in db.CreateHeraRoleInput) (*db.HeraRole, error)
	AddHeraBlock(blockedRoleID, blockerRoleID int64) error
	CreateHeraPlan(orchID int64, nodes []db.HeraPlannedNodeSpec, edges []db.HeraBlockSpec) ([]*db.HeraRole, error)
	HeraRoleHasBinding(roleID int64) (bool, error)
	UpdateHeraPlannedNode(roleID int64, prompt, project string) error
	CancelHeraPlannedNode(roleID int64) error
	RemoveHeraBlock(blockedRoleID, blockerRoleID int64) error
}

// SpawnInput is the payload handed to a Spawner. Moved here (from
// internal/mcp) so SpawnWorker can call it without an import cycle;
// internal/mcp type-aliases HeraSpawnInput to this.
type SpawnInput struct {
	Project        string // resolved argus project (input override or coordinator's task project)
	BaseName       string // base worker role name; the daemon uniquifies within the orchestrator
	TaskPrompt     string // orientation-prefixed prompt delivered to the worker session
	RolePrompt     string // verbatim user prompt, stored on the role row for the Details pane
	Branch         string // optional base branch passed through to CreateAndStart
	Backend        string // optional backend override
	Model          string // optional per-worker model override (empty = backend default)
	OrchestratorID int64  // orchestrator the new worker role + binding belong to
}

// SpawnResult is the success payload from a Spawner.
type SpawnResult struct {
	Task    *model.Task
	Role    *db.HeraRole
	Binding *db.HeraBinding
}

// Spawner performs the transactional born-bound worker spawn inside the
// daemon. internal/mcp type-aliases HeraSpawner to this.
type Spawner func(SpawnInput) (*SpawnResult, error)

// ResolveProject defaults an explicit project override to the coordinator's
// own task project. Returns ErrNoProject when both are blank. Shared by
// spawn-worker and every plan-node creation path — both resolve project the
// same way.
func ResolveProject(explicit, coordinatorProject string) (string, error) {
	project := strings.TrimSpace(explicit)
	if project == "" {
		project = strings.TrimSpace(coordinatorProject)
	}
	if project == "" {
		return "", ErrNoProject
	}
	return project, nil
}

// SpawnWorkerParams bundles hera_spawn_worker's post-coordinator-guard
// inputs — everything needed once the caller's coordinator role has already
// been resolved (from cwd via MCP, or from orch_id via REST).
type SpawnWorkerParams struct {
	OrchID             int64
	OrchName           string
	CoordinatorName    string
	CoordinatorProject string // the coordinator's own task's project; the project default
	RoleName           string
	Prompt             string
	Project            string
	Branch             string
	Backend            string
	Model              string
}

// SpawnWorker resolves the project + role-name defaults, builds the
// orientation-prefixed task prompt, and invokes spawn. Returns the resolved
// project alongside the spawn result so callers can render it without
// recomputing the same default. spawn == nil is a caller bug (both
// internal/mcp and internal/api guard on a configured spawner before calling
// this) but is checked defensively rather than panicking.
func SpawnWorker(spawn Spawner, p SpawnWorkerParams) (*SpawnResult, string, error) {
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return nil, "", ErrPromptRequired
	}
	if spawn == nil {
		return nil, "", fmt.Errorf("hera spawn not configured")
	}
	project, err := ResolveProject(p.Project, p.CoordinatorProject)
	if err != nil {
		return nil, "", err
	}
	baseName := strings.TrimSpace(p.RoleName)
	if baseName == "" {
		baseName = agent.DeriveHeraWorkerName(prompt)
	}
	taskPrompt := agent.HeraWorkerOrientation(p.OrchName, p.CoordinatorName) + "\n\n---\n\n" + prompt
	res, err := spawn(SpawnInput{
		Project:        project,
		BaseName:       baseName,
		TaskPrompt:     taskPrompt,
		RolePrompt:     prompt,
		Branch:         p.Branch,
		Backend:        p.Backend,
		Model:          p.Model,
		OrchestratorID: p.OrchID,
	})
	if err != nil {
		return nil, "", err
	}
	return res, project, nil
}

// ResolvePlanNodeKind resolves a plan-node kind + its delivery prompt from
// raw (kind, prompt, goal) input — shared by hera_plan_node's and hera_plan's
// per-node validation. Kind defaults to worker when blank; a subcoord node's
// delivery prompt is its goal, a worker node's is its prompt.
func ResolvePlanNodeKind(kind, prompt, goal string) (db.HeraNodeKind, string, error) {
	nodeKind := db.HeraNodeKindWorker
	if strings.TrimSpace(kind) == string(db.HeraNodeKindSubCoord) {
		nodeKind = db.HeraNodeKindSubCoord
	}
	if nodeKind == db.HeraNodeKindSubCoord {
		g := strings.TrimSpace(goal)
		if g == "" {
			return "", "", ErrGoalRequired
		}
		return nodeKind, g, nil
	}
	p := strings.TrimSpace(prompt)
	if p == "" {
		return "", "", ErrPromptRequired
	}
	return nodeKind, p, nil
}

// CreatePlanNode creates one planned node under orchID — the shared
// post-coordinator-guard body of hera_plan_node: resolves the project
// default, uniquifies name within the orchestrator, and inserts the row.
func CreatePlanNode(store MutationStore, orchID int64, coordinatorProject, name string, nodeKind db.HeraNodeKind, prompt, project string) (*db.HeraRole, error) {
	resolvedProject, err := ResolveProject(project, coordinatorProject)
	if err != nil {
		return nil, err
	}
	uniqueName, err := store.UniqueHeraRoleName(orchID, name)
	if err != nil {
		return nil, fmt.Errorf("uniquify name: %w", err)
	}
	role, err := store.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID,
		Name:           uniqueName,
		NodeKind:       nodeKind,
		ArgusProject:   resolvedProject,
		Prompt:         prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("create planned node: %w", err)
	}
	return role, nil
}

// PlanNodeSpec mirrors hera_plan's per-node request shape, pre-validation.
type PlanNodeSpec struct {
	Name    string
	Kind    string
	Prompt  string
	Goal    string
	Project string
}

// PlanEdgeSpec mirrors hera_plan's per-edge request shape. Both endpoints are
// names — either an in-batch node's name or an existing role's current name —
// since in-batch nodes have no id until the transaction commits.
type PlanEdgeSpec struct {
	Blocked string
	Blocker string
}

// CreatePlan validates and creates a whole plan graph (nodes then edges) in
// one transaction — the shared post-coordinator-guard body of hera_plan,
// reused verbatim by the REST whole-graph endpoint (both address edges by
// name for exactly this reason). resolveExisting resolves an edge-endpoint
// name that is NOT present in this batch to a pre-existing role, scoped to
// the calling orchestrator; callers supply their own store lookup.
func CreatePlan(store MutationStore, orchID int64, coordinatorProject string, nodes []PlanNodeSpec, edges []PlanEdgeSpec, resolveExisting func(name string) (*db.HeraRole, error)) ([]*db.HeraRole, error) {
	specs := make([]db.HeraPlannedNodeSpec, 0, len(nodes))
	nameIdx := map[string]int{}
	for i, n := range nodes {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			return nil, fmt.Errorf("nodes[%d]: name is required", i)
		}
		nodeKind, prompt, err := ResolvePlanNodeKind(n.Kind, n.Prompt, n.Goal)
		if err != nil {
			switch {
			case errors.Is(err, ErrGoalRequired):
				return nil, fmt.Errorf("nodes[%d] (%s): subcoord node requires a goal", i, name)
			case errors.Is(err, ErrPromptRequired):
				return nil, fmt.Errorf("nodes[%d] (%s): prompt is required", i, name)
			default:
				return nil, fmt.Errorf("nodes[%d] (%s): %w", i, name, err)
			}
		}
		project, err := ResolveProject(n.Project, coordinatorProject)
		if err != nil {
			return nil, fmt.Errorf("nodes[%d] (%s): no project resolved", i, name)
		}
		uniqueName, err := store.UniqueHeraRoleName(orchID, name)
		if err != nil {
			return nil, fmt.Errorf("nodes[%d] (%s): uniquify: %w", i, name, err)
		}
		specs = append(specs, db.HeraPlannedNodeSpec{Name: uniqueName, ArgusProject: project, Prompt: prompt, NodeKind: nodeKind})
		nameIdx[name] = i
	}

	edgeSpecs := make([]db.HeraBlockSpec, 0, len(edges))
	for i, e := range edges {
		blockedIdx, blockedID, err := resolvePlanEndpoint(nameIdx, e.Blocked, resolveExisting, fmt.Sprintf("edges[%d].blocked", i))
		if err != nil {
			return nil, err
		}
		blockerIdx, blockerID, err := resolvePlanEndpoint(nameIdx, e.Blocker, resolveExisting, fmt.Sprintf("edges[%d].blocker", i))
		if err != nil {
			return nil, err
		}
		edgeSpecs = append(edgeSpecs, db.HeraBlockSpec{
			BlockedNodeIdx: blockedIdx, BlockedRoleID: blockedID,
			BlockerNodeIdx: blockerIdx, BlockerRoleID: blockerID,
		})
	}

	return store.CreateHeraPlan(orchID, specs, edgeSpecs)
}

// resolvePlanEndpoint resolves an edge endpoint name to either an in-batch
// node (returning its batch index, roleID 0) or a pre-existing role
// (returning index -1 and the role id). In-batch names take precedence.
func resolvePlanEndpoint(nameIdx map[string]int, name string, resolveExisting func(string) (*db.HeraRole, error), field string) (int, int64, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("%s: name is required", field)
	}
	if idx, ok := nameIdx[trimmed]; ok {
		return idx, 0, nil
	}
	role, err := resolveExisting(trimmed)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", field, err)
	}
	return -1, role.ID, nil
}

// UpdatePlanNode edits a planned node's prompt/project — the shared
// post-coordinator-guard body of hera_plan_node_update.
func UpdatePlanNode(store MutationStore, roleID int64, prompt, project string) error {
	prompt = strings.TrimSpace(prompt)
	project = strings.TrimSpace(project)
	if prompt == "" && project == "" {
		return ErrEmptyPlanUpdate
	}
	has, err := store.HeraRoleHasBinding(roleID)
	if err != nil {
		return fmt.Errorf("check materialization: %w", err)
	}
	if has {
		return ErrAlreadyMaterialized
	}
	return store.UpdateHeraPlannedNode(roleID, prompt, project)
}

// CancelPlanNode cancels a planned node — the shared post-coordinator-guard
// body of hera_plan_node_cancel.
func CancelPlanNode(store MutationStore, roleID int64) error {
	has, err := store.HeraRoleHasBinding(roleID)
	if err != nil {
		return fmt.Errorf("check materialization: %w", err)
	}
	if has {
		return ErrAlreadyMaterialized
	}
	return store.CancelHeraPlannedNode(roleID)
}

// AddBlock adds a blocking edge — the shared (thin) post-coordinator-guard
// body of hera_block. internal/mcp resolves both endpoints by name;
// internal/api resolves them by id; both call this once resolved.
func AddBlock(store MutationStore, blockedRoleID, blockerRoleID int64) error {
	return store.AddHeraBlock(blockedRoleID, blockerRoleID)
}

// RemoveBlock removes a blocking edge — the shared (thin) post-coordinator-
// guard body of hera_unblock. Idempotent: removing a non-existent edge
// succeeds.
func RemoveBlock(store MutationStore, blockedRoleID, blockerRoleID int64) error {
	return store.RemoveHeraBlock(blockedRoleID, blockerRoleID)
}
