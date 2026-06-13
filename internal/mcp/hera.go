package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

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
	// Bindings
	HeraLiveBindingByTask(taskID string) (*db.HeraBinding, error)
	HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error)
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	// Role status
	UpsertHeraRoleStatus(roleID int64, status db.HeraRoleStatusValue) error
	// Inbox count for hera_join claim response (does NOT cancel deliveries).
	HeraInbox(roleID int64) ([]*db.HeraMessage, error)
	// Task meta mirror (best-effort soft-fail).
	SetMeta(taskID, namespace, key, value string) error
}

// heraToolDefs contains the 9 hera_* tool schemas, ported verbatim from
// Hera's daemon.toolDefinitions() — same param names, descriptions, and
// required lists as the external Hera daemon so agents have an identical
// surface when running natively.
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
		Description: "Spawn a new born-bound worker task under this orchestrator. Caller must hold a live coordinator binding. Creates an argus task (worktree + session) and, transactionally, a worker role + binding pre-bound to it. An orientation prefix naming the coordinator + orchestrator is prepended to the prompt; the verbatim prompt is stored on the role. Defaults the project to the coordinator's own.",
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
			},
			"required": []string{"cwd", "prompt"},
		},
	},
	{
		Name:        "hera_tree_updates",
		Description: "Retrieve a rolled-up view of the orchestrator subtree since a message cursor. (M5 stub — not yet implemented natively.)",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Absolute path of the calling agent's working directory"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "Orchestrator name (optional if the task has one live binding)"},
				"since":        map[string]interface{}{"type": "integer", "description": "Message ID cursor; omit to use stored cursor"},
			},
			"required": []string{"cwd"},
		},
	},
	{
		Name:        "hera_get_messages",
		Description: "Fetch full message bodies for specific message IDs. Access is restricted to messages within the caller's orchestrator (M3; full subtree in M5).",
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
		baseName = deriveHeraWorkerName(prompt)
	}

	// Prepend the orientation prefix so the worker knows it is born-bound and
	// who its coordinator + orchestrator are. The verbatim user prompt follows
	// the separator and is also stored on the role row.
	taskPrompt := heraWorkerOrientation(caller.orch.Name, caller.role.Name) + "\n\n---\n\n" + prompt

	res, err := s.heraSpawn(HeraSpawnInput{
		Project:        project,
		BaseName:       baseName,
		TaskPrompt:     taskPrompt,
		RolePrompt:     prompt,
		Branch:         p.Branch,
		Backend:        p.Backend,
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

// heraWorkerNameRe matches runs of ASCII lowercase letters and digits, used to
// build a URL-slug-style role name from a prompt.
var heraWorkerNameRe = regexp.MustCompile(`[a-z0-9]+`)

// deriveHeraWorkerName produces a slug from the first 40 chars of the prompt,
// mirroring Hera's swDeriveWorkerName. Returns "worker" for empty/symbol input.
func deriveHeraWorkerName(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	lower := strings.Map(func(r rune) rune { return unicode.ToLower(r) }, string(runes))
	tokens := heraWorkerNameRe.FindAllString(lower, -1)
	if len(tokens) == 0 {
		return "worker"
	}
	slug := strings.Join(tokens, "-")
	if slug == "" {
		return "worker"
	}
	return slug
}

// heraWorkerOrientation is the orientation prefix prepended to a spawned
// worker's prompt. Ports Hera's spawn-handler guidance verbatim (hera_send for
// progress, sub-coordinator escalation, iris for PRs), augmented to name the
// orchestrator and state that the worker is born-bound.
func heraWorkerOrientation(orchestrator, coordinator string) string {
	return fmt.Sprintf(
		"You are a worker agent born bound to hera orchestrator %q under coordinator %q. "+
			"You may report progress via hera_send. If this task requires changes to another repo "+
			"or you need to spawn sub-agents, call hera_new_orchestrator(cwd=$PWD, name=\"...\", "+
			"coordinator_role_name=\"coord\", prompt=\"...\") to become a sub-coordinator, then use "+
			"hera_spawn_worker(project=\"TARGET-PROJECT\", ...) to dispatch workers in that project. "+
			"When opening pull requests, use mcp__argus__iris_gh_pr_create (not gh pr create directly) "+
			"so argus records the PR URL and the hera rail shows the PR indicator.",
		orchestrator, coordinator)
}

func (s *Server) toolHeraTreeUpdates(id interface{}, _ json.RawMessage) *Response {
	return toolError(id, "native hera_tree_updates lands in M5 (subtree roll-up)")
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

	msgs, err := s.heraSvc.GetByIDs(p.IDs)
	if err != nil {
		return toolError(id, fmt.Sprintf("get messages failed: %v", err))
	}

	// Build id → message map for O(1) per-ID lookup.
	byID := make(map[int64]*db.HeraMessage, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}

	type msgEntry = map[string]interface{}
	results := make([]msgEntry, 0, len(p.IDs))
	for _, reqID := range p.IDs {
		m := byID[reqID]
		if m == nil {
			results = append(results, msgEntry{"id": reqID, "error": "not found"})
			continue
		}
		// Access rule (M3): from_role or to_role must be in caller's orchestrator.
		// M5 expands this to the full subtree via SubtreeOrchIDs BFS.
		if !s.heraMessageInOrch(m, caller.orch.ID) {
			results = append(results, msgEntry{"id": reqID, "error": "access denied: message not in caller's orchestrator"})
			continue
		}
		fromName := fmt.Sprintf("role:%d", m.FromRoleID)
		if fromRole, rErr := s.heraStore.HeraRole(m.FromRoleID); rErr == nil {
			fromName = fromRole.Name
		}
		toName := fmt.Sprintf("role:%d", m.ToRoleID)
		if toRole, rErr := s.heraStore.HeraRole(m.ToRoleID); rErr == nil {
			toName = toRole.Name
		}
		entry := msgEntry{
			"id":        m.ID,
			"from_role": fromName,
			"to_role":   toName,
			"sent_at":   m.SentAt.Format(time.RFC3339),
			"tldr":      m.Tldr,
			"body":      m.Body,
		}
		if m.InReplyTo != nil {
			entry["in_reply_to"] = *m.InReplyTo
		}
		results = append(results, entry)
	}

	out, _ := json.Marshal(map[string]interface{}{"messages": results})
	return toolResult(id, string(out))
}

// heraMessageInOrch returns true when the message's sender or recipient role
// belongs to orchID. M3 access rule — M5 will expand to subtree traversal.
func (s *Server) heraMessageInOrch(m *db.HeraMessage, orchID int64) bool {
	if fromRole, err := s.heraStore.HeraRole(m.FromRoleID); err == nil && fromRole.OrchestratorID == orchID {
		return true
	}
	if toRole, err := s.heraStore.HeraRole(m.ToRoleID); err == nil && toRole.OrchestratorID == orchID {
		return true
	}
	return false
}
