package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
)

// HeraStore is the subset of *db.DB used by the native hera_* MCP tools.
// Defined as an interface so tests can inject a fake without spinning up SQLite.
type HeraStore interface {
	// Orchestrators
	CreateHeraOrchestrator(name string) (*db.HeraOrchestrator, error)
	HeraOrchestrator(id int64) (*db.HeraOrchestrator, error)
	HeraOrchestratorByName(name string) (*db.HeraOrchestrator, error)
	// Roles
	HeraRole(id int64) (*db.HeraRole, error)
	HeraRoleByName(orchID int64, name string) (*db.HeraRole, error)
	ListHeraRolesByKind(orchID int64, kind db.HeraRoleKind) ([]*db.HeraRole, error)
	CreateHeraRoleWithBinding(roleIn db.CreateHeraRoleInput, taskID, worktreePath string) (*db.HeraRole, *db.HeraBinding, error)
	// Plan-DAG authoring (add-hera-plan-substrate). Coordinator-only at the tool
	// layer; the store enforces cycle + same-orchestrator constraints.
	CreateHeraPlannedRole(in db.CreateHeraRoleInput) (*db.HeraRole, error)
	AddHeraBlock(blockedRoleID, blockerRoleID int64) error
	UniqueHeraRoleName(orchID int64, base string) (string, error)
	// Bindings
	HeraLiveBindingByTask(taskID string) (*db.HeraBinding, error)
	HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error)
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	// Role status
	UpsertHeraRoleStatus(roleID int64, status db.HeraRoleStatusValue) error
	// Inbox count for hera_join claim response (does NOT cancel deliveries).
	HeraInbox(roleID int64) ([]*db.HeraMessage, error)
	// Subtree roll-up + per-role tree cursor (M5).
	SubtreeOrchIDs(rootOrchID int64) ([]int64, error)
	HeraTreeUpdatesSince(rootOrchID, since int64) ([]db.HeraMessageTLDR, int64, error)
	GetHeraTreeCursor(roleID int64) (int64, error)
	SetHeraTreeCursor(roleID, cursor int64) error
	// Task meta mirror (best-effort soft-fail).
	SetMeta(taskID, namespace, key, value string) error
	// RollHeraWorkerToReview is the BUG-050 close-out roll shared with the
	// session-exit hooks; the hera_status("done") trigger calls it. No-op
	// unless the task is a live worker AND currently in_progress.
	RollHeraWorkerToReview(taskID string) (bool, error)
}

// heraToolDefs contains the 12 hera_* tool schemas. The first 9 are ported
// verbatim from Hera's daemon.toolDefinitions() — same param names,
// descriptions, and required lists as the external Hera daemon so agents have an
// identical surface when running natively. The last 3 (hera_plan_node /
// hera_block / hera_plan) are the native plan-DAG authoring tools
// (add-hera-plan-substrate); they are coordinator-only like hera_spawn_worker.
var heraToolDefs = []Tool{
	{
		Name:        "hera_new_orchestrator",
		Description: "Bootstrap a new Hera orchestrator and claim the coordinator role for the calling task. Call this once to start a multi-agent coordination session. The coordinator is automatically bound to the calling task's argus worktree.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":                   map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"name":                  map[string]interface{}{"type": "string", "description": "Orchestrator name (e.g., the project / feature being coordinated)"},
				"coordinator_role_name": map[string]interface{}{"type": "string", "description": "Name for the coordinator role under the new orchestrator (e.g., 'coord' or 'foo-coordinator')"},
				"prompt":                map[string]interface{}{"type": "string", "description": "(optional) Coordinator's prompt, free-form prose"},
			},
			"required": []string{"cwd", "name", "coordinator_role_name"},
		},
	},
	{
		Name:        "hera_join",
		Description: "Claim an existing hera role already bound to this task (claim mode, role_name omitted), or attach as a new worker/freelance role (attach mode, role_name + kind supplied). Use hera_new_orchestrator to become a coordinator.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional in claim mode for tasks with exactly one binding; required for tasks with 2+ bindings or for attach mode) The orchestrator to claim from or attach to."},
				"role_name":    map[string]interface{}{"type": "string", "description": "(attach mode only) Self-chosen role name"},
				"kind":         map[string]interface{}{"type": "string", "enum": []string{"worker", "freelance"}, "description": "(attach mode only) Role kind. coordinator is not accepted here — use hera_new_orchestrator."},
				"prompt":       map[string]interface{}{"type": "string", "description": "(optional, attach mode) Role prompt, free-form prose"},
				"status":       map[string]interface{}{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "(optional, attach mode) Initial role status"},
			},
			"required": []string{"cwd"},
		},
	},
	{
		Name:        "hera_send",
		Description: "Send a message to another role in the same orchestrator. Workers/freelancers default to the coordinator when 'to' is omitted. Coordinators must supply an explicit 'to'.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"body":         map[string]interface{}{"type": "string", "description": "Message body"},
				"tldr":         map[string]interface{}{"type": "string", "description": "One-line summary of the message (≤120 chars, required)"},
				"to":           map[string]interface{}{"type": "string", "description": "(optional for worker/freelance, required for coordinator) Recipient role name within the same orchestrator"},
				"in_reply_to":  map[string]interface{}{"type": "integer", "description": "(optional) Message id this is a reply to"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the sender role for this call. The recipient is resolved within the same orchestrator."},
			},
			"required": []string{"cwd", "body", "tldr"},
		},
	},
	{
		Name:        "hera_inbox",
		Description: "Read unread messages addressed to the caller's hera role. Marks messages as read (cancels pending doorbell deliveries). Returns oldest first.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
			},
			"required": []string{"cwd"},
		},
	},
	{
		Name:        "hera_mark_read",
		Description: "Mark specific hera messages as read and cancel their pending doorbell deliveries.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"message_ids":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "Inbox message ids returned by hera_inbox"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
			},
			"required": []string{"cwd", "message_ids"},
		},
	},
	{
		Name:        "hera_status",
		Description: "Update the calling role's status within its orchestrator. Also mirrors the status to the argus task_meta sidecar (best-effort).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"status":       map[string]interface{}{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "New role status"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
			},
			"required": []string{"cwd", "status"},
		},
	},
	{
		Name:        "hera_spawn_worker",
		Description: "Spawn a new born-bound worker task under this orchestrator. Caller must hold a live coordinator binding. Creates an argus task (worktree + session) and, transactionally, a worker role + binding pre-bound to it. An orientation prefix naming the coordinator + orchestrator is prepended to the prompt; the verbatim prompt is stored on the role. Defaults the project to the coordinator's own. Pass `model` to match the worker's model to its task complexity (e.g. a strong model for a hard refactor, a cheaper/faster one for mechanical work).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
				"role_name":    map[string]interface{}{"type": "string", "description": "(optional) Worker role name. Derived from prompt slug if omitted; made unique within the orchestrator automatically"},
				"prompt":       map[string]interface{}{"type": "string", "description": "Full task prompt delivered to the new worker session. An orientation prefix naming the coordinator is prepended automatically. The verbatim prompt is also stored on the role row"},
				"project":      map[string]interface{}{"type": "string", "description": "(optional) Override the argus project. Defaults to the coordinator's own project"},
				"branch":       map[string]interface{}{"type": "string", "description": "(optional) Branch passed to argus CreateTask. Defaults to project default"},
				"backend":      map[string]interface{}{"type": "string", "description": "(optional) Backend passed to argus CreateTask. Defaults to project default"},
				"model":        map[string]interface{}{"type": "string", "description": "(optional) Per-worker model override; choose by task complexity. Must be valid for the worker's resolved backend (claude: opus/sonnet/haiku; codex: e.g. gpt-5; pi: its model ids). Empty = backend default. Only claude/codex/pi backends receive --model; ignored if the backend command already hard-codes --model"},
			},
			"required": []string{"cwd", "prompt"},
		},
	},
	{
		Name:        "hera_tree_updates",
		Description: "Scan the caller's orchestrator subtree for new messages since a cursor. Returns TLDR-only subject lines — no bodies. Call hera_get_messages for full content on IDs of interest. Cursor is stored per-role and auto-advances; pass `since` to override.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Absolute path of the calling agent's working directory"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "Orchestrator name (optional if the task has one live binding)"},
				"since":        map[string]interface{}{"type": "integer", "description": "Message ID cursor; omit to use (and auto-advance) the stored per-role cursor"},
			},
			"required": []string{"cwd"},
		},
	},
	{
		Name:        "hera_get_messages",
		Description: "Fetch full message bodies by ID. Use after hera_tree_updates to drill into messages of interest. Access is restricted to messages within the caller's orchestrator subtree; inaccessible or missing IDs get a per-ID error field rather than a top-level error.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Absolute path of the calling agent's working directory"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "Orchestrator name (optional if the task has one live binding)"},
				"ids":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "Message IDs to fetch"},
			},
			"required": []string{"cwd", "ids"},
		},
	},
	{
		Name:        "hera_plan_node",
		Description: "Create a PLANNED node: a worker role with NO live agent, worktree, or inbox yet — a durable plan entry costing one row. Coordinator-only. The node materializes into a live worker automatically once all its blockers (declared via hera_block) reach role-status done. Bake a stable short-id prefix into the name (e.g. '2c-fact-checker': number = serial stage, letter = parallel member). The prompt is delivered to the worker on materialization (a check-in standing-order is prepended automatically).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"name":         map[string]interface{}{"type": "string", "description": "Short-id-prefixed node name (e.g. '2c-fact-checker'); made unique within the orchestrator automatically"},
				"prompt":       map[string]interface{}{"type": "string", "description": "Task prompt delivered to the worker when the node materializes"},
				"project":      map[string]interface{}{"type": "string", "description": "(optional) argus project for the worker. Defaults to the coordinator's own project"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "name", "prompt"},
		},
	},
	{
		Name:        "hera_block",
		Description: "Add a blocking edge between two roles in the same orchestrator: `blocked` waits on `blocker` reaching role-status done before it materializes. Coordinator-only. Rejected if it would create a cycle or if the two roles are in different orchestrators (v1 sequences sub-teams at the parent level instead). Roles are addressed by name within the caller's orchestrator.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"blocked":      map[string]interface{}{"type": "string", "description": "Name of the role that WAITS (the dependent)"},
				"blocker":      map[string]interface{}{"type": "string", "description": "Name of the role that must reach role-status done FIRST"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "blocked", "blocker"},
		},
	},
	{
		Name:        "hera_plan",
		Description: "Submit a whole plan graph in one transactional call: a set of planned nodes plus the blocking edges among them. Coordinator-only. Nodes are created first, then edges (cycle-checked, single-orchestrator) reference nodes by name. Either the whole graph is created or, on any cycle/cross-orchestrator/validation error, nothing is. Use this to author a multi-phase plan at once instead of many hera_plan_node + hera_block calls.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd": map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"nodes": map[string]interface{}{
					"type":        "array",
					"description": "Planned nodes. Each: {name (short-id-prefixed), prompt, project (optional)}",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":    map[string]interface{}{"type": "string"},
							"prompt":  map[string]interface{}{"type": "string"},
							"project": map[string]interface{}{"type": "string"},
						},
						"required": []string{"name", "prompt"},
					},
				},
				"edges": map[string]interface{}{
					"type":        "array",
					"description": "Blocking edges. Each: {blocked, blocker} naming nodes (by name) in this plan or existing roles in the orchestrator",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"blocked": map[string]interface{}{"type": "string"},
							"blocker": map[string]interface{}{"type": "string"},
						},
						"required": []string{"blocked", "blocker"},
					},
				},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "nodes"},
		},
	},
}

// callerRoleResult holds the resolved context for the calling agent's hera
// role. Returned by resolveCallerRole; consumed by every hera tool that
// requires the caller to already be bound to a role.
type callerRoleResult struct {
	task    *model.Task
	binding *db.HeraBinding
	role    *db.HeraRole
	orch    *db.HeraOrchestrator
}

// SetHeraService wires the native hera_* MCP tools. Must be called before
// ListenAndServe. Mirrors the SetMessageManager precedent: fields are read at
// request time without a mutex, so all Set* calls must precede server start.
//
// spawner may be nil — the other hera tools still work, but hera_spawn_worker
// returns a "spawn not configured" error. The daemon supplies a non-nil
// spawner that runs the transactional born-bound create.
func (s *Server) SetHeraService(svc *hera.Service, store HeraStore, spawner HeraSpawner) {
	s.heraSvc = svc
	s.heraStore = store
	s.heraSpawn = spawner
}

// heraEnabled returns true when the hera service is wired AND task management
// is also enabled — caller resolution via cwd requires task management.
func (s *Server) heraEnabled() bool {
	return s.heraSvc != nil && s.taskMgmtEnabled()
}

// resolveCallerRole maps cwd to an argus task, then to the caller's live hera
// binding+role. orchestratorName disambiguates when the task holds multiple
// live bindings. On ErrHeraAmbiguous the returned error lists the caller's
// orchestrator options.
func (s *Server) resolveCallerRole(cwd, orchestratorName string) (*callerRoleResult, error) {
	task, err := s.resolveTask("", cwd)
	if err != nil {
		return nil, err
	}

	if orchestratorName != "" {
		orch, err := s.heraStore.HeraOrchestratorByName(orchestratorName)
		if errors.Is(err, db.ErrHeraNotFound) {
			return nil, fmt.Errorf("unknown orchestrator %q", orchestratorName)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve orchestrator: %w", err)
		}
		binding, err := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID)
		if errors.Is(err, db.ErrHeraNotFound) {
			return nil, fmt.Errorf("task has no live hera binding under orchestrator %q; call hera_join first", orchestratorName)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve binding: %w", err)
		}
		role, err := s.heraStore.HeraRole(binding.RoleID)
		if err != nil {
			return nil, fmt.Errorf("resolve role: %w", err)
		}
		return &callerRoleResult{task: task, binding: binding, role: role, orch: orch}, nil
	}

	binding, err := s.heraStore.HeraLiveBindingByTask(task.ID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("this argus task is not bound to any hera role; call hera_join with orchestrator+role_name+kind to attach, or hera_new_orchestrator to create one")
	}
	if errors.Is(err, db.ErrHeraAmbiguous) {
		return nil, s.buildHeraAmbiguousError(task.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve binding: %w", err)
	}

	role, err := s.heraStore.HeraRole(binding.RoleID)
	if err != nil {
		return nil, fmt.Errorf("resolve role: %w", err)
	}
	orch, err := s.heraStore.HeraOrchestrator(binding.OrchestratorID)
	if err != nil {
		return nil, fmt.Errorf("resolve orchestrator: %w", err)
	}
	return &callerRoleResult{task: task, binding: binding, role: role, orch: orch}, nil
}

// buildHeraAmbiguousError returns a disambiguation error listing the
// orchestrator names bound to taskID.
func (s *Server) buildHeraAmbiguousError(taskID string) error {
	bindings, listErr := s.heraStore.ListHeraLiveBindingsByTask(taskID)
	if listErr != nil {
		return fmt.Errorf("task holds multiple hera bindings; re-call with orchestrator=<name> to disambiguate (list unavailable: %w)", listErr)
	}
	names := make([]string, 0, len(bindings))
	for _, b := range bindings {
		o, oErr := s.heraStore.HeraOrchestrator(b.OrchestratorID)
		if oErr == nil {
			names = append(names, o.Name)
		} else {
			names = append(names, fmt.Sprintf("id:%d", b.OrchestratorID))
		}
	}
	return fmt.Errorf("task holds live bindings in multiple orchestrators (%s); re-call with orchestrator=<name> to disambiguate", strings.Join(names, ", "))
}

func (s *Server) toolHeraNewOrchestrator(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd                 string `json:"cwd"`
		Name                string `json:"name"`
		CoordinatorRoleName string `json:"coordinator_role_name"`
		Prompt              string `json:"prompt"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if p.Name == "" {
		return toolError(id, "name is required")
	}
	if p.CoordinatorRoleName == "" {
		return toolError(id, "coordinator_role_name is required")
	}

	task, err := s.resolveTask("", p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Create (or fetch existing) orchestrator — CreateHeraOrchestrator is idempotent.
	orch, err := s.heraStore.CreateHeraOrchestrator(p.Name)
	if err != nil {
		return toolError(id, fmt.Sprintf("create orchestrator: %v", err))
	}

	// Reject if this task already holds a live binding under the target orchestrator.
	existing, checkErr := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID)
	if checkErr == nil && existing != nil {
		return toolError(id, fmt.Sprintf(
			"task already has a live binding under orchestrator %q (binding_id=%d); use hera_join to retrieve your current role",
			p.Name, existing.ID))
	}

	role, binding, err := s.heraStore.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           p.CoordinatorRoleName,
		Kind:           db.HeraKindCoordinator,
		// M4 fix: persist the coordinator's argus project on the role so
		// downstream spawn/adopt can default a worker's project without a
		// task lookup. Historical rows may be empty; consumers tolerate that.
		ArgusProject: task.Project,
		Prompt:       p.Prompt,
	}, task.ID, task.Worktree)
	if err != nil {
		return toolError(id, fmt.Sprintf("create coordinator role: %v", err))
	}

	// Mirror to task_meta best-effort — failure must never undo local state.
	if metaErr := s.heraStore.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "tool", "hera_new_orchestrator", "task_id", task.ID, "err", metaErr)
	}

	slog.Info("[hera] new_orchestrator ok", "orch", orch.Name, "role", role.Name, "binding_id", binding.ID, "task_id", task.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "Orchestrator created.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", orch.Name)
	fmt.Fprintf(&b, "- **role_name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **kind**: coordinator\n")
	fmt.Fprintf(&b, "- **binding_id**: %d\n", binding.ID)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", task.ID)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraJoin(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
		RoleName     string `json:"role_name"`
		Kind         string `json:"kind"`
		Prompt       string `json:"prompt"`
		Status       string `json:"status"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}

	if p.RoleName == "" {
		// Claim mode: retrieve the existing binding + unread count.
		caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
		if err != nil {
			return toolError(id, err.Error())
		}
		// HeraInbox does NOT cancel deliveries — that is intentional for claim mode.
		msgs, _ := s.heraStore.HeraInbox(caller.role.ID)
		unread := len(msgs)

		var b strings.Builder
		fmt.Fprintf(&b, "Joined (claim mode).\n\n")
		fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
		fmt.Fprintf(&b, "- **role_name**: %s\n", caller.role.Name)
		fmt.Fprintf(&b, "- **kind**: %s\n", caller.role.Kind)
		fmt.Fprintf(&b, "- **binding_id**: %d\n", caller.binding.ID)
		fmt.Fprintf(&b, "- **unread_message_count**: %d\n", unread)
		return toolResult(id, b.String())
	}

	// Attach mode: create a new role+binding.
	if p.Kind == string(db.HeraKindCoordinator) {
		return toolError(id, "coordinator kind is not accepted here; use hera_new_orchestrator to bootstrap a new orchestrator")
	}
	if p.Kind != string(db.HeraKindWorker) && p.Kind != string(db.HeraKindFreelance) {
		return toolError(id, "kind must be 'worker' or 'freelance'")
	}
	if p.Orchestrator == "" {
		return toolError(id, "orchestrator is required in attach mode")
	}

	task, err := s.resolveTask("", p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	orch, err := s.heraStore.HeraOrchestratorByName(p.Orchestrator)
	if errors.Is(err, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("unknown orchestrator %q", p.Orchestrator))
	}
	if err != nil {
		return toolError(id, fmt.Sprintf("resolve orchestrator: %v", err))
	}

	// Reject if already bound to this orchestrator.
	existing, checkErr := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID)
	if checkErr == nil && existing != nil {
		return toolError(id, fmt.Sprintf("task already has a live binding under orchestrator %q; call hera_join(cwd) without role_name to retrieve your current role", p.Orchestrator))
	}

	role, binding, err := s.heraStore.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           p.RoleName,
		Kind:           db.HeraRoleKind(p.Kind),
		// M4 fix: persist the attaching task's argus project on the role.
		ArgusProject: task.Project,
		Prompt:       p.Prompt,
	}, task.ID, task.Worktree)
	if err != nil {
		return toolError(id, fmt.Sprintf("create role: %v", err))
	}

	// Optional initial status.
	if p.Status != "" {
		sv := db.HeraRoleStatusValue(p.Status)
		if uErr := s.heraStore.UpsertHeraRoleStatus(role.ID, sv); uErr != nil {
			slog.Warn("[hera] upsert initial status failed", "role_id", role.ID, "err", uErr)
		}
	}

	// Mirror to task_meta best-effort.
	if metaErr := s.heraStore.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(role.Kind)); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "tool", "hera_join", "task_id", task.ID, "err", metaErr)
	}

	slog.Info("[hera] join (attach) ok", "orch", orch.Name, "role", role.Name, "kind", role.Kind, "binding_id", binding.ID, "task_id", task.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "Joined (attach mode).\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", orch.Name)
	fmt.Fprintf(&b, "- **role_name**: %s\n", role.Name)
	fmt.Fprintf(&b, "- **kind**: %s\n", role.Kind)
	fmt.Fprintf(&b, "- **binding_id**: %d\n", binding.ID)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", task.ID)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraSend(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Body         string `json:"body"`
		Tldr         string `json:"tldr"`
		To           string `json:"to"`
		InReplyTo    *int64 `json:"in_reply_to"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if p.Body == "" {
		return toolError(id, "body is required")
	}
	if p.Tldr == "" {
		return toolError(id, "tldr is required")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Resolve recipient.
	var toRole *db.HeraRole
	if p.To != "" {
		toRole, err = s.heraStore.HeraRoleByName(caller.orch.ID, p.To)
		if errors.Is(err, db.ErrHeraNotFound) {
			return toolError(id, fmt.Sprintf("recipient role %q not found in orchestrator %q", p.To, caller.orch.Name))
		}
		if err != nil {
			return toolError(id, fmt.Sprintf("resolve recipient: %v", err))
		}
	} else {
		switch caller.role.Kind {
		case db.HeraKindWorker, db.HeraKindFreelance:
			// Default to the active coordinator.
			coordinators, listErr := s.heraStore.ListHeraRolesByKind(caller.orch.ID, db.HeraKindCoordinator)
			if listErr != nil {
				return toolError(id, fmt.Sprintf("list coordinators: %v", listErr))
			}
			if len(coordinators) == 0 {
				return toolError(id, "no active coordinator found in orchestrator; supply an explicit 'to' recipient")
			}
			toRole = coordinators[0]
		case db.HeraKindCoordinator:
			return toolError(id, "coordinator senders must supply an explicit 'to' recipient")
		default:
			return toolError(id, "cannot determine default recipient for unknown role kind; supply an explicit 'to'")
		}
	}

	msg, err := s.heraSvc.Send(caller.role.ID, toRole.ID, p.Body, p.Tldr, p.InReplyTo)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrHeraMessageBodyTooLarge):
			return toolError(id, "body exceeds 64 KiB")
		case errors.Is(err, db.ErrHeraMessageSelfSend):
			return toolError(id, "cannot send a message to self")
		case errors.Is(err, db.ErrHeraMessageInboxFull):
			return toolError(id, "recipient inbox is full (500 unread cap)")
		case errors.Is(err, db.ErrHeraMessageRateLimited):
			return toolError(id, "sender rate limit exceeded (50/min)")
		case errors.Is(err, db.ErrHeraMessageTldrRequired):
			return toolError(id, "tldr is required")
		case errors.Is(err, db.ErrHeraMessageTldrTooLong):
			return toolError(id, "tldr exceeds 120 characters")
		case errors.Is(err, db.ErrHeraMessageRecipientInvalid):
			return toolError(id, "recipient role is missing or archived")
		default:
			slog.Warn("[hera] send failed", "from_role_id", caller.role.ID, "to_role_id", toRole.ID, "err", err)
			return toolError(id, fmt.Sprintf("send failed: %v", err))
		}
	}

	slog.Info("[hera] send ok", "msg_id", msg.ID, "from_role", caller.role.Name, "to_role", toRole.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Message sent.\n\n")
	fmt.Fprintf(&b, "- **message_id**: %d\n", msg.ID)
	fmt.Fprintf(&b, "- **to**: %s\n", toRole.Name)
	fmt.Fprintf(&b, "- **delivery_mode**: %s\n", msg.DeliveryMode)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraInbox(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Service.Inbox fetches unread messages and cancels pending doorbell deliveries.
	msgs, err := s.heraSvc.Inbox(caller.role.ID)
	if err != nil {
		return toolError(id, fmt.Sprintf("inbox query failed: %v", err))
	}

	// Mark all returned messages as read (matches tool description: "Marks messages
	// as read"). Inbox cancels deliveries; MarkRead stamps read_at.
	if len(msgs) > 0 {
		ids := make([]int64, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		if _, markErr := s.heraSvc.MarkRead(caller.role.ID, ids); markErr != nil {
			slog.Warn("[hera] inbox: mark-read failed", "role_id", caller.role.ID, "err", markErr)
		}
	}

	if len(msgs) == 0 {
		return toolResult(id, fmt.Sprintf("Inbox empty for role %q.", caller.role.Name))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Inbox for role %q (%d message%s):\n", caller.role.Name, len(msgs), plural(len(msgs)))
	for _, m := range msgs {
		fromName := fmt.Sprintf("role:%d", m.FromRoleID)
		if fromRole, rErr := s.heraStore.HeraRole(m.FromRoleID); rErr == nil {
			fromName = fromRole.Name
		}
		fmt.Fprintf(&b, "\n--- %d ---\n", m.ID)
		fmt.Fprintf(&b, "from: %s\n", fromName)
		if m.InReplyTo != nil {
			fmt.Fprintf(&b, "in_reply_to: %d\n", *m.InReplyTo)
		}
		fmt.Fprintf(&b, "sent_at: %s\n", m.SentAt.Format(time.RFC3339))
		fmt.Fprintf(&b, "tldr: %s\n", m.Tldr)
		fmt.Fprintf(&b, "body:\n%s\n", m.Body)
	}
	return toolResult(id, b.String())
}

func (s *Server) toolHeraMarkRead(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string  `json:"cwd"`
		MessageIDs   []int64 `json:"message_ids"`
		Orchestrator string  `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if len(p.MessageIDs) == 0 {
		return toolError(id, "message_ids is required (non-empty list)")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	n, err := s.heraSvc.MarkRead(caller.role.ID, p.MessageIDs)
	if err != nil {
		return toolError(id, fmt.Sprintf("mark read failed: %v", err))
	}

	return toolResult(id, fmt.Sprintf("Marked %d of %d message ID%s as read.", n, len(p.MessageIDs), plural(len(p.MessageIDs))))
}

func (s *Server) toolHeraStatus(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Status       string `json:"status"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if p.Status == "" {
		return toolError(id, "status is required")
	}

	sv := db.HeraRoleStatusValue(p.Status)
	switch sv {
	case db.HeraStatusIdle, db.HeraStatusWorking, db.HeraStatusBlocked, db.HeraStatusDone:
	default:
		return toolError(id, fmt.Sprintf("invalid status %q; must be one of idle, working, blocked, done", p.Status))
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	if err := s.heraStore.UpsertHeraRoleStatus(caller.role.ID, sv); err != nil {
		return toolError(id, fmt.Sprintf("update status failed: %v", err))
	}

	// Mirror to task_meta best-effort.
	if metaErr := s.heraStore.SetMeta(caller.binding.ArgusTaskID, db.HeraMetaNamespace, db.HeraMetaKeyThreadStatus, p.Status); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "tool", "hera_status", "task_id", caller.binding.ArgusTaskID, "err", metaErr)
	}

	// BUG-050 PRIMARY trigger: a WORKER reporting status="done" rolls its bound
	// task to in_review + stamps ready_to_close — the idle-but-done case the
	// exit hook misses (Claude workers finish their report and go idle, they
	// don't exit). Worker-kind ONLY (coordinators/freelance just update status);
	// RollHeraWorkerToReview itself no-ops unless the task is in_progress, so it
	// never clobbers a human-set in_review/complete and never auto-completes. It
	// touches DB status + meta only — the live session is left running. Failure
	// is soft (logged, never surfaced) so the status update always succeeds; the
	// call is idempotent (re-calling done is a no-op once flipped).
	if sv == db.HeraStatusDone && caller.role.Kind == db.HeraKindWorker {
		if flipped, rErr := s.heraStore.RollHeraWorkerToReview(caller.binding.ArgusTaskID); rErr != nil {
			slog.Warn("[hera] status(done): worker roll failed (status still updated)", "task_id", caller.binding.ArgusTaskID, "err", rErr)
		} else if flipped {
			slog.Info("[hera] status(done): rolled worker task to in_review", "task_id", caller.binding.ArgusTaskID, "role", caller.role.Name)
		}
	}

	slog.Info("[hera] status ok", "role", caller.role.Name, "status", p.Status, "orch", caller.orch.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Status updated.\n\n")
	fmt.Fprintf(&b, "- **role**: %s\n", caller.role.Name)
	fmt.Fprintf(&b, "- **status**: %s\n", p.Status)
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraSpawnWorker(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	if s.heraSpawn == nil {
		return toolError(id, "hera spawn not configured (daemon did not wire a spawner)")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
		RoleName     string `json:"role_name"`
		Prompt       string `json:"prompt"`
		Project      string `json:"project"`
		Branch       string `json:"branch"`
		Backend      string `json:"backend"`
		Model        string `json:"model"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return toolError(id, "prompt is required")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}
	if caller.role.Kind != db.HeraKindCoordinator {
		return toolError(id, fmt.Sprintf(
			"caller role %q has kind %q; only coordinators may spawn workers",
			caller.role.Name, caller.role.Kind))
	}

	// Project: explicit override first, then the COORDINATOR'S OWN TASK project
	// (caller.task.Project — authoritative). We deliberately do NOT trust
	// role.ArgusProject here: historical roles created before the M4 fix have an
	// empty argus_project, and the live task row is always correct.
	project := strings.TrimSpace(p.Project)
	if project == "" {
		project = caller.task.Project
	}
	if project == "" {
		return toolError(id, "no project resolved (coordinator task has no project and none was supplied)")
	}

	// Base worker role name: explicit role_name, else a slug of the prompt. The
	// daemon spawner uniquifies it within the orchestrator (suffix -2, -3, …).
	baseName := strings.TrimSpace(p.RoleName)
	if baseName == "" {
		baseName = agent.DeriveHeraWorkerName(prompt)
	}

	// Prepend the orientation prefix so the worker knows it is born-bound and
	// who its coordinator + orchestrator are. The verbatim user prompt follows
	// the separator and is also stored on the role row.
	taskPrompt := agent.HeraWorkerOrientation(caller.orch.Name, caller.role.Name) + "\n\n---\n\n" + prompt

	res, err := s.heraSpawn(HeraSpawnInput{
		Project:        project,
		BaseName:       baseName,
		TaskPrompt:     taskPrompt,
		RolePrompt:     prompt,
		Branch:         p.Branch,
		Backend:        p.Backend,
		Model:          strings.TrimSpace(p.Model),
		OrchestratorID: caller.orch.ID,
	})
	if err != nil {
		return toolError(id, fmt.Sprintf("spawn worker: %v", err))
	}

	slog.Info("[hera] spawn_worker ok",
		"orch", caller.orch.Name, "role", res.Role.Name, "binding_id", res.Binding.ID,
		"task_id", res.Task.ID, "coordinator", caller.role.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Worker spawned.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	fmt.Fprintf(&b, "- **role_name**: %s\n", res.Role.Name)
	fmt.Fprintf(&b, "- **kind**: %s\n", res.Role.Kind)
	fmt.Fprintf(&b, "- **binding_id**: %d\n", res.Binding.ID)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", res.Task.ID)
	fmt.Fprintf(&b, "- **project**: %s\n", project)
	return toolResult(id, b.String())
}

func (s *Server) toolHeraTreeUpdates(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
		Since        *int64 `json:"since"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Effective cursor: an explicit `since` overrides (and does NOT advance) the
	// stored per-role cursor; otherwise read the stored cursor and auto-advance.
	explicit := p.Since != nil
	var cursor int64
	if explicit {
		cursor = *p.Since
	} else {
		cursor, err = s.heraStore.GetHeraTreeCursor(caller.role.ID)
		if err != nil {
			return toolError(id, fmt.Sprintf("read tree cursor: %v", err))
		}
	}

	msgs, nextCursor, err := s.heraStore.HeraTreeUpdatesSince(caller.orch.ID, cursor)
	if err != nil {
		return toolError(id, fmt.Sprintf("tree updates: %v", err))
	}

	// Auto-advance the stored cursor unless the caller pinned an explicit `since`.
	// An empty result leaves the cursor unchanged (nextCursor == cursor).
	if !explicit {
		if uErr := s.heraStore.SetHeraTreeCursor(caller.role.ID, nextCursor); uErr != nil {
			slog.Warn("[hera] tree_updates: cursor advance failed (results still returned)",
				"role_id", caller.role.ID, "err", uErr)
		}
	}

	// Resolve role + orchestrator names in the handler (TLDR projection carries
	// ids only). Cache lookups — a busy subtree repeats senders/recipients.
	roleCache := map[int64]*db.HeraRole{}
	orchNameCache := map[int64]string{}
	resolveRole := func(rid int64) *db.HeraRole {
		if r, ok := roleCache[rid]; ok {
			return r
		}
		r, rErr := s.heraStore.HeraRole(rid)
		if rErr != nil {
			r = nil
		}
		roleCache[rid] = r
		return r
	}
	resolveOrchName := func(oid int64) string {
		if n, ok := orchNameCache[oid]; ok {
			return n
		}
		n := fmt.Sprintf("orch:%d", oid)
		if o, oErr := s.heraStore.HeraOrchestrator(oid); oErr == nil {
			n = o.Name
		}
		orchNameCache[oid] = n
		return n
	}

	type lineEntry = map[string]interface{}
	lines := make([]lineEntry, 0, len(msgs))
	for _, m := range msgs {
		fromName := fmt.Sprintf("role:%d", m.FromRoleID)
		fromOrch := ""
		if r := resolveRole(m.FromRoleID); r != nil {
			fromName = r.Name
			fromOrch = resolveOrchName(r.OrchestratorID)
		}
		toName := fmt.Sprintf("role:%d", m.ToRoleID)
		toOrch := ""
		if r := resolveRole(m.ToRoleID); r != nil {
			toName = r.Name
			toOrch = resolveOrchName(r.OrchestratorID)
		}
		lines = append(lines, lineEntry{
			"id":                m.ID,
			"from_role":         fromName,
			"from_orchestrator": fromOrch,
			"to_role":           toName,
			"to_orchestrator":   toOrch,
			"tldr":              m.Tldr,
			"sent_at":           m.SentAt.Format(time.RFC3339),
		})
	}

	out, _ := json.Marshal(map[string]interface{}{
		"count":       len(lines),
		"next_cursor": nextCursor,
		"messages":    lines,
	})
	return toolResult(id, string(out))
}

func (s *Server) toolHeraGetMessages(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string  `json:"cwd"`
		IDs          []int64 `json:"ids"`
		Orchestrator string  `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if len(p.IDs) == 0 {
		return toolError(id, "ids is required (non-empty list)")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Access scope (M5): the caller may read any message whose sender OR recipient
	// role lives in the caller's orchestrator SUBTREE (M3 restricted this to the
	// caller's single orchestrator). Resolve the subtree once, up front.
	orchIDs, err := s.heraStore.SubtreeOrchIDs(caller.orch.ID)
	if err != nil {
		return toolError(id, fmt.Sprintf("resolve subtree: %v", err))
	}
	subtree := make(map[int64]struct{}, len(orchIDs))
	for _, oid := range orchIDs {
		subtree[oid] = struct{}{}
	}

	msgs, err := s.heraSvc.GetByIDs(p.IDs)
	if err != nil {
		return toolError(id, fmt.Sprintf("get messages failed: %v", err))
	}

	// Build id → message map for O(1) per-ID lookup.
	byID := make(map[int64]*db.HeraMessage, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}

	orchNameCache := map[int64]string{}
	resolveOrchName := func(oid int64) string {
		if n, ok := orchNameCache[oid]; ok {
			return n
		}
		n := fmt.Sprintf("orch:%d", oid)
		if o, oErr := s.heraStore.HeraOrchestrator(oid); oErr == nil {
			n = o.Name
		}
		orchNameCache[oid] = n
		return n
	}

	type msgEntry = map[string]interface{}
	results := make([]msgEntry, 0, len(p.IDs))
	for _, reqID := range p.IDs {
		m := byID[reqID]
		if m == nil {
			results = append(results, msgEntry{"id": reqID, "error": "not found"})
			continue
		}
		// Resolve sender/recipient roles for both the access check and the names.
		fromRole, _ := s.heraStore.HeraRole(m.FromRoleID)
		toRole, _ := s.heraStore.HeraRole(m.ToRoleID)

		// Access rule (M5): sender OR recipient role must be in the caller's
		// orchestrator SUBTREE (M3 restricted this to the caller's single orch).
		if !heraRoleInSubtree(fromRole, subtree) && !heraRoleInSubtree(toRole, subtree) {
			results = append(results, msgEntry{"id": reqID, "error": "access denied: message not in caller's subtree"})
			continue
		}

		fromName := fmt.Sprintf("role:%d", m.FromRoleID)
		fromOrch := ""
		if fromRole != nil {
			fromName = fromRole.Name
			fromOrch = resolveOrchName(fromRole.OrchestratorID)
		}
		toName := fmt.Sprintf("role:%d", m.ToRoleID)
		toOrch := ""
		if toRole != nil {
			toName = toRole.Name
			toOrch = resolveOrchName(toRole.OrchestratorID)
		}
		entry := msgEntry{
			"id":                m.ID,
			"from_role":         fromName,
			"from_orchestrator": fromOrch,
			"to_role":           toName,
			"to_orchestrator":   toOrch,
			"sent_at":           m.SentAt.Format(time.RFC3339),
			"tldr":              m.Tldr,
			"body":              m.Body,
		}
		if m.InReplyTo != nil {
			entry["in_reply_to"] = *m.InReplyTo
		}
		results = append(results, entry)
	}

	out, _ := json.Marshal(map[string]interface{}{"messages": results})
	return toolResult(id, string(out))
}

// heraRoleInSubtree reports whether role is non-nil and its orchestrator is in
// the subtree set. The M5 access predicate for hera_get_messages.
func heraRoleInSubtree(role *db.HeraRole, subtree map[int64]struct{}) bool {
	if role == nil {
		return false
	}
	_, ok := subtree[role.OrchestratorID]
	return ok
}
