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
	CreateHeraOrchestrator(name, baseBranch string) (*db.HeraOrchestrator, error)
	HeraOrchestrator(id int64) (*db.HeraOrchestrator, error)
	HeraOrchestratorByName(name string) (*db.HeraOrchestrator, error)
	// Roles
	HeraRole(id int64) (*db.HeraRole, error)
	HeraRoleByName(orchID int64, name string) (*db.HeraRole, error)
	ListHeraRolesByKind(orchID int64, kind db.HeraRoleKind) ([]*db.HeraRole, error)
	CreateHeraRoleWithBinding(roleIn db.CreateHeraRoleInput, taskID, worktreePath string) (*db.HeraRole, *db.HeraBinding, error)
	// MoveHeraBinding is the move-capable counterpart to CreateHeraRoleWithBinding
	// (fix-hera-join-move-binding): ends oldBindingID and creates the new
	// role+binding under a different orchestrator, in one transaction.
	MoveHeraBinding(oldBindingID int64, roleIn db.CreateHeraRoleInput, taskID, worktreePath string) (*db.MoveHeraBindingResult, error)
	// Plan-DAG authoring (add-hera-plan-substrate). Coordinator-only at the tool
	// layer; the store enforces cycle + same-orchestrator constraints.
	CreateHeraPlannedRole(in db.CreateHeraRoleInput) (*db.HeraRole, error)
	AddHeraBlock(blockedRoleID, blockerRoleID int64) error
	// CreateHeraPlan creates a whole plan graph (all nodes + all edges) in ONE
	// transaction — either the whole graph is created or, on any error, nothing
	// is. Edge endpoints reference in-batch nodes by index or pre-existing roles
	// by id (see db.HeraBlockSpec).
	CreateHeraPlan(orchID int64, nodes []db.HeraPlannedNodeSpec, edges []db.HeraBlockSpec) ([]*db.HeraRole, error)
	UniqueHeraRoleName(orchID int64, base string) (string, error)
	// Plan-mutation verbs (make-hera-plan-living D5). Coordinator-only at the
	// tool layer; the materialized-vs-planned guard lives in the MCP handlers.
	HeraRoleHasBinding(roleID int64) (bool, error)
	UpdateHeraPlannedNode(roleID int64, prompt, project string) error
	CancelHeraPlannedNode(roleID int64) error
	RemoveHeraBlock(blockedRoleID, blockerRoleID int64) error
	// Bindings
	HeraLiveBindingByTask(taskID string) (*db.HeraBinding, error)
	HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error)
	// HeraLiveBindingByWorktreeAndOrchestrator and HeraLiveBindingByWorktree
	// are the worktree-keyed fallback used by resolveCallerRole and the
	// attach/bootstrap collision guards (BUG-059): a cwd that resolves to a
	// stale/colliding argus task still has the correct worktree_path, so a
	// worktree-keyed lookup finds the live binding a task-keyed lookup missed.
	HeraLiveBindingByWorktreeAndOrchestrator(worktreePath string, orchID int64) (*db.HeraBinding, error)
	HeraLiveBindingByWorktree(worktreePath string) (*db.HeraBinding, error)
	HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error)
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	// EndHeraBinding and CreateHeraBinding back hera_rebind's end-stale +
	// insert-clean reconciliation (BUG-059 repair path).
	EndHeraBinding(bindingID int64, reason string) error
	CreateHeraBinding(in db.CreateHeraBindingInput) (*db.HeraBinding, error)
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
	// RollHeraWorkerFailed rolls a failed worker's task to in_review WITHOUT
	// stamping ready_to_close (D2, make-hera-plan-living). Called when a worker
	// reports status="failed"; same invariants as RollHeraWorkerToReview.
	RollHeraWorkerFailed(taskID string) (bool, error)
}

// heraToolDefs contains the 18 hera_* tool schemas. The first 9 are ported
// verbatim from Hera's daemon.toolDefinitions() — same param names,
// descriptions, and required lists as the external Hera daemon so agents have an
// identical surface when running natively. hera_move (fix-hera-join-move-binding)
// is native-only — the external daemon has no equivalent. The next 3 (hera_plan_node /
// hera_block / hera_plan) are the native plan-DAG authoring tools
// (add-hera-plan-substrate); they are coordinator-only like hera_spawn_worker.
// The next 3 (hera_plan_node_update / hera_unblock / hera_plan_node_cancel) are
// the plan-mutation verbs (make-hera-plan-living D5). The last (hera_revive) is
// the coordinator-only PULL-revive tool (add-hera-revive); its gating logic
// lives in the shared internal/hera.ReviveRole primitive.
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
				"base_branch":           map[string]interface{}{"type": "string", "description": "(optional) Explicit base branch that this plan-DAG's ROOT nodes stack on. When omitted, root nodes default to the coordinator's own branch (then the project default). Has no effect on nodes that have blockers."},
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
		Name:        "hera_move",
		Description: "Relocate the caller's live hera binding to a different orchestrator: ends the current binding (end_reason 'moved') and creates a new worker/freelance role+binding under the target orchestrator, in one transaction. Use this instead of hera_join when already bound elsewhere — hera_join attach mode rejects and redirects here.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":               map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"orchestrator":      map[string]interface{}{"type": "string", "description": "Target orchestrator to move the binding to"},
				"role_name":         map[string]interface{}{"type": "string", "description": "Name for the new role under the target orchestrator"},
				"kind":              map[string]interface{}{"type": "string", "enum": []string{"worker", "freelance"}, "description": "Role kind. coordinator is not accepted here — use hera_new_orchestrator."},
				"from_orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates which of the caller's live bindings to move, when the task holds 2+ live bindings"},
				"status":            map[string]interface{}{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "(optional) Initial status for the new role"},
			},
			"required": []string{"cwd", "orchestrator", "role_name", "kind"},
		},
	},
	{
		Name:        "hera_rebind",
		Description: "Repair a stuck/ambiguous hera binding without tearing down the argus session. Use when a born-bound worker can neither claim its binding (hera_join claim says none) nor attach a new one (hera_join attach hits a UNIQUE constraint / worktree-collision error) — the sign that a reused worktree path left the live binding pointing at a stale argus task. Reconciles the binding for the given orchestrator to the caller's real live task so both lookup paths agree; the role (and its prompt, messages, status) is preserved. Refuses when the state is genuinely ambiguous (two live in_progress tasks share the worktree, or multiple roles are bound here and no role_name is given).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "The orchestrator whose binding for this worktree should be reconciled"},
				"role_name":    map[string]interface{}{"type": "string", "description": "(optional) Required only when more than one role holds a live binding at this worktree; names the role to reconcile"},
			},
			"required": []string{"cwd", "orchestrator"},
		},
	},
	{
		Name:        "hera_send",
		Description: "Send a message to another role in the same orchestrator. Workers/freelancers default to the coordinator when 'to' is omitted. Coordinators must supply an explicit 'to'. Worker/freelance senders MUST supply 'status' (one of idle/working/blocked/done/failed) — it is applied to the sender's role synchronously before the message is sent.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"body":         map[string]interface{}{"type": "string", "description": "Message body"},
				"tldr":         map[string]interface{}{"type": "string", "description": "One-line summary of the message (≤120 chars, required)"},
				"to":           map[string]interface{}{"type": "string", "description": "(optional for worker/freelance, required for coordinator) Recipient role name within the same orchestrator"},
				"in_reply_to":  map[string]interface{}{"type": "integer", "description": "(optional) Message id this is a reply to"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the sender role for this call. The recipient is resolved within the same orchestrator."},
				"status":       map[string]interface{}{"type": "string", "enum": []string{"idle", "working", "blocked", "done", "failed"}, "description": "REQUIRED for worker/freelance senders: the sender's current role status, applied synchronously before the message is sent. Optional for coordinator senders (omit to leave status unchanged)."},
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
		Description: "Update the calling role's status within its orchestrator. Also mirrors the status to the argus task_meta sidecar (best-effort). handoff_note and request_recycle let ANY hera-bound role (coordinator, worker, or freelance) record distilled context and signal a self-service recycle in the same call — the daemon defers the actual kill-and-restart until the session goes idle.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":             map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"status":          map[string]interface{}{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "New role status"},
				"orchestrator":    map[string]interface{}{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
				"handoff_note":    map[string]interface{}{"type": "string", "description": "Short free-text distilled context, overwritten into task_meta(hera, handoff_note) in the same call. Accepted from any hera-bound role kind."},
				"request_recycle": map[string]interface{}{"type": "boolean", "description": "When true, records a pending-recycle intent for the caller's task, consumed by the recycle_coord primitive once the session goes idle. Accepted from any hera-bound role kind."},
			},
			"required": []string{"cwd", "status"},
		},
	},
	{
		Name:        "hera_revive",
		Description: "PULL-revive a hera role this coordinator coordinates. If its session is dead (no live process) it is restarted in place (--session-id resume when the task has one). If it's alive but genuinely stuck (idle, NOT blocked on a user prompt) its session is stopped and resumed in place at its existing size. A live coordinator role, a busy (actively working) role, one parked at a question, or one with a restart already in flight is left untouched and reported as such — this can never thrash a session that is actually working or waiting on an answer. Coordinator-only. Use when hera_status/hera_tree_updates show no progress from a role — e.g. after a session-supervisor restart SIGHUPs its PTY. This is pull/on-demand: nothing calls this automatically.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				"role_name":    map[string]interface{}{"type": "string", "description": "Name of the role to revive, within the caller's orchestrator. Must not be the caller's own role."},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "role_name"},
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
				"prompt":       map[string]interface{}{"type": "string", "description": "The worker's MISSION/task only. An orientation prefix naming the coordinator is prepended automatically; the verbatim prompt is stored on the role row and shown as the node's description in the plan-DAG view. Do NOT prepend organization or security policy: every spawned worker session receives its org instructions independently (harness-injected), so a prepended copy is redundant and pollutes the stored prompt and the plan-DAG view"},
				"project":      map[string]interface{}{"type": "string", "description": "(optional) Override the argus project. Defaults to the coordinator's own project"},
				"branch":       map[string]interface{}{"type": "string", "description": "(optional) Branch passed to argus CreateTask. Defaults to project default"},
				"backend":      map[string]interface{}{"type": "string", "description": "(optional) Backend passed to argus CreateTask. Defaults to project default"},
				"model":        map[string]interface{}{"type": "string", "description": "(optional) Per-worker model override; choose by task complexity. Must be valid for the worker's resolved backend (claude: opus/sonnet/haiku/fable; codex: e.g. gpt-5; pi: its model ids). Empty = backend default. Only claude/codex/pi backends receive --model; ignored if the backend command already hard-codes --model"},
				"archetype":    map[string]interface{}{"type": "string", "description": "(optional) Diligence archetype for the worker (e.g. code_slice, bug_fix, big_build, review, ci_loop). Selects the per-archetype model from the project's bound profile and is exported as ARGUS_ARCHETYPE to the worker. Defaults to code_slice when omitted"},
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
				"prompt":       map[string]interface{}{"type": "string", "description": "The worker's MISSION/task only, delivered when the node materializes and stored verbatim as the node's description in the plan-DAG view. Do NOT prepend organization or security policy: every spawned worker session receives its org instructions independently (harness-injected), so a prepended copy is redundant and pollutes the stored prompt and the plan-DAG view"},
				"project":      map[string]interface{}{"type": "string", "description": "(optional) argus project for the worker. Defaults to the coordinator's own project"},
				"archetype":    map[string]interface{}{"type": "string", "description": "(optional) Diligence archetype for the worker (e.g. code_slice, review, ci_loop); persisted on the planned node and copied onto the task when it materializes"},
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
					"description": "Planned nodes. Each: {name (short-id-prefixed), prompt, project (optional), archetype (optional)}",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":      map[string]interface{}{"type": "string"},
							"prompt":    map[string]interface{}{"type": "string"},
							"project":   map[string]interface{}{"type": "string"},
							"archetype": map[string]interface{}{"type": "string", "description": "(optional) Diligence archetype; copied onto the task when the node materializes"},
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
	{
		Name:        "hera_plan_node_update",
		Description: "Edit a PLANNED node's prompt and/or project. Coordinator-only. Rejected if the node has already materialized (prompt already delivered to a running worker). Requires at least one of prompt or project. Re-pointing a prompt before a node materializes lets a coordinator reconcile the plan to reality without cancelling and re-creating.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"name":         map[string]interface{}{"type": "string", "description": "Name of the planned node to update"},
				"prompt":       map[string]interface{}{"type": "string", "description": "(optional) New prompt to deliver when the node materializes. Preserves existing if omitted."},
				"project":      map[string]interface{}{"type": "string", "description": "(optional) Override the argus project for the node. Preserves existing if omitted."},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "name"},
		},
	},
	{
		Name:        "hera_unblock",
		Description: "Remove a blocking edge between two roles in the orchestrator: `blocker` no longer gates `blocked`. Coordinator-only. Idempotent — removing a non-existent edge succeeds as a no-op. Re-pointing an edge is hera_unblock + hera_block; there is no separate re-point verb.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"blocked":      map[string]interface{}{"type": "string", "description": "Name of the role that WAITS (the dependent)"},
				"blocker":      map[string]interface{}{"type": "string", "description": "Name of the role whose blocking edge to remove"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "blocked", "blocker"},
		},
	},
	{
		Name:        "hera_plan_node_cancel",
		Description: "Cancel a PLANNED node: stamps cancelled_at, excludes it from materialization, and unblocks its dependents (a cancelled node no longer gates them). Coordinator-only. The node is kept in the plan for visibility (renders as grey ✕). Rejected if the node has already materialized — stop a running worker via the task lifecycle instead.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":          map[string]interface{}{"type": "string", "description": "Coordinator's worktree path (use $PWD)"},
				"name":         map[string]interface{}{"type": "string", "description": "Name of the planned node to cancel"},
				"orchestrator": map[string]interface{}{"type": "string", "description": "(optional) Disambiguates when the calling task holds multiple live coordinator bindings"},
			},
			"required": []string{"cwd", "name"},
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

// SetHeraReviver wires the hera_revive MCP tool (add-hera-revive) to the
// daemon's shared PULL-revive primitive. A new, independent setter (rather
// than a fourth SetHeraService parameter) matching the Server's existing
// multi-setter pattern (SetClipboard, SetScheduleManager, ...). Must be
// called before ListenAndServe, like every other Set* method. reviver may be
// nil — hera_revive then returns a "revive not configured" error rather than
// panicking.
func (s *Server) SetHeraReviver(reviver HeraReviver) {
	s.heraRevive = reviver
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
		binding, err := s.liveBindingForOrch(task, orch.ID)
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

	binding, err := s.liveBindingForTask(task)
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

// liveBindingForOrch resolves the caller's live binding under orchID. It keys
// first on the resolved argus_task_id (the exact, historical path) and, on a
// miss, falls back to the caller's worktree_path. The fallback closes the
// BUG-059 gap: when cwd resolved to a colliding/stale task id, the task-keyed
// lookup misses the live binding, but the (worktree_path, orchestrator_id)
// live-uniqueness guarantees the worktree-keyed lookup finds the one binding
// an attach INSERT would collide with. Orchestrator scoping makes the
// fallback safe — a stale binding for a DIFFERENT orchestrator sharing the
// worktree is never returned.
func (s *Server) liveBindingForOrch(task *model.Task, orchID int64) (*db.HeraBinding, error) {
	bnd, err := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orchID)
	if err == nil {
		return bnd, nil
	}
	if !errors.Is(err, db.ErrHeraNotFound) {
		return nil, err
	}
	if task.Worktree == "" {
		return nil, db.ErrHeraNotFound
	}
	return s.heraStore.HeraLiveBindingByWorktreeAndOrchestrator(task.Worktree, orchID)
}

// liveBindingForTask resolves the caller's live binding with no orchestrator
// given to disambiguate. Keys first on argus_task_id and, on a miss, falls
// back to worktree_path (BUG-059) via the same ambiguous-aware single-row
// lookup HeraLiveBindingByTask uses (ErrHeraAmbiguous on 2+ live bindings
// across different orchestrators sharing the worktree), so a genuine
// multi-binding collision still surfaces for disambiguation instead of
// silently picking one.
func (s *Server) liveBindingForTask(task *model.Task) (*db.HeraBinding, error) {
	bnd, err := s.heraStore.HeraLiveBindingByTask(task.ID)
	if err == nil || errors.Is(err, db.ErrHeraAmbiguous) {
		return bnd, err
	}
	if !errors.Is(err, db.ErrHeraNotFound) {
		return nil, err
	}
	if task.Worktree == "" {
		return nil, db.ErrHeraNotFound
	}
	return s.heraStore.HeraLiveBindingByWorktree(task.Worktree)
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
		BaseBranch          string `json:"base_branch"`
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

	// Guard the self-promotion footgun: a task that already holds a live
	// coordinator binding under a DIFFERENT orchestrator must not bind its own
	// session as a SECOND coordinator of a new one. Following such a call, the
	// rail renders a phantom nested "sub-coordinator" that drives the identical
	// PTY as its parent — and in practice that pseudo-sub-coordinator implements
	// solo instead of dispatching. Run this BEFORE CreateHeraOrchestrator so a
	// rejected call never leaves an orphan orchestrator behind. Scope: a
	// coordinator re-calling for the SAME orchestrator falls through to the
	// same-orchestrator guard below (which keeps its hera_join guidance, and
	// creates no orphan since that orchestrator already exists); callers holding
	// only worker/freelance bindings (worker self-promotion) or no binding (fresh
	// bootstrap) also fall through. Fail-open on a list error, matching the
	// sibling same-orchestrator guard.
	if liveBindings, lbErr := s.heraStore.ListHeraLiveBindingsByTask(task.ID); lbErr == nil {
		for _, b := range liveBindings {
			r, rErr := s.heraStore.HeraRole(b.RoleID)
			if rErr != nil || r.Kind != db.HeraKindCoordinator {
				continue
			}
			orchName := fmt.Sprintf("id:%d", b.OrchestratorID)
			if o, oErr := s.heraStore.HeraOrchestrator(b.OrchestratorID); oErr == nil {
				orchName = o.Name
			}
			if orchName == p.Name {
				// Same orchestrator: not a self-promotion — let the same-orchestrator
				// guard below handle it with its hera_join guidance.
				continue
			}
			slog.Info("[hera] new_orchestrator rejected: caller already a coordinator",
				"caller_task", task.ID, "coordinator_orch", orchName, "requested_orch", p.Name)
			return toolError(id, fmt.Sprintf(
				"task is already the coordinator of orchestrator %q; a coordinator DISPATCHES work "+
					"with hera_spawn_worker (project= targets any repo) and must not bind its own session "+
					"as a second coordinator. For a dedicated sub-team, spawn a worker (which can promote "+
					"itself via hera_new_orchestrator) or author a kind=subcoord hera_plan_node.",
				orchName))
		}
	}

	// Create (or fetch existing) orchestrator — CreateHeraOrchestrator is idempotent.
	orch, err := s.heraStore.CreateHeraOrchestrator(p.Name, p.BaseBranch)
	if err != nil {
		return toolError(id, fmt.Sprintf("create orchestrator: %v", err))
	}

	// Reject if this task already holds a live binding under the target orchestrator.
	existing, checkErr := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID)
	if checkErr == nil && existing != nil {
		return toolError(id, fmt.Sprintf(
			"task already has a live binding under orchestrator %q (binding_id=%d); use hera_join to retrieve your current role",
			p.Name, existing.ID))
	} else if checkErr != nil && !errors.Is(checkErr, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("lookup existing binding: %v", checkErr))
	}

	// Reject if this WORKTREE already holds a live binding under the target
	// orchestrator, even when the task-keyed check above missed (BUG-059): a
	// reused worktree path can make cwd resolve to a stale/colliding task id
	// while the binding INSERT below is still constrained by (worktree_path,
	// orchestrator_id) uniqueness. Pre-checking it here yields an actionable
	// resume hint instead of a raw constraint error.
	if task.Worktree != "" {
		if _, wtErr := s.heraStore.HeraLiveBindingByWorktreeAndOrchestrator(task.Worktree, orch.ID); wtErr == nil {
			return toolError(id, fmt.Sprintf(
				"this worktree already holds a live binding to orchestrator %q; resume via hera_join(cwd, orchestrator=%q) instead",
				p.Name, p.Name))
		} else if !errors.Is(wtErr, db.ErrHeraNotFound) {
			return toolError(id, fmt.Sprintf("lookup existing worktree binding: %v", wtErr))
		}
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

	// Reject if already bound to this orchestrator — either keyed by the
	// resolved argus_task_id OR by the worktree path. Bindings to OTHER
	// orchestrators are handled by the different-orchestrator guard below.
	existing, checkErr := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID)
	if checkErr == nil && existing != nil {
		return toolError(id, fmt.Sprintf("task already has a live binding under orchestrator %q; call hera_join(cwd) without role_name to retrieve your current role", p.Orchestrator))
	} else if checkErr != nil && !errors.Is(checkErr, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("lookup existing binding: %v", checkErr))
	}

	// The worktree-keyed check is what keeps attach in agreement with claim
	// under BUG-059: a reused worktree path can make cwd resolve to a
	// stale/colliding task id, so the task-keyed check above misses a binding
	// that the (worktree_path, orchestrator_id) uniqueness will nonetheless
	// reject on the INSERT below. Pre-checking it here converts that raw
	// constraint error into an actionable message — claim it, or hera_rebind
	// when the existing binding's argus_task_id has drifted from the caller's.
	if task.Worktree != "" {
		if wtExisting, wtErr := s.heraStore.HeraLiveBindingByWorktreeAndOrchestrator(task.Worktree, orch.ID); wtErr == nil {
			hint := fmt.Sprintf("call hera_join(cwd, orchestrator=%q) with no role_name to claim it", p.Orchestrator)
			if wtExisting.ArgusTaskID != task.ID {
				hint += fmt.Sprintf(
					"; if delivery to this worker is broken (the binding still points at stale argus task %s), call hera_rebind(cwd, orchestrator=%q) to reconcile it",
					wtExisting.ArgusTaskID, p.Orchestrator)
			}
			return toolError(id, fmt.Sprintf(
				"this worktree already holds a live binding to orchestrator %q; %s", p.Orchestrator, hint))
		} else if !errors.Is(wtErr, db.ErrHeraNotFound) {
			return toolError(id, fmt.Sprintf("lookup existing worktree binding: %v", wtErr))
		}
	}

	// Reject if bound under a DIFFERENT orchestrator — attach mode is for an
	// unbound task or a fresh orchestrator, not for relocating an existing
	// membership. By this point any live binding cannot be under the target
	// (handled above), so a non-empty list here is necessarily elsewhere.
	// Fail-open on a list error, matching the sibling guard in
	// toolHeraNewOrchestrator.
	if liveBindings, lbErr := s.heraStore.ListHeraLiveBindingsByTask(task.ID); lbErr == nil && len(liveBindings) > 0 {
		return toolError(id, fmt.Sprintf(
			"task already holds a live hera binding under a different orchestrator; use hera_move to relocate it to %q instead of hera_join",
			p.Orchestrator))
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

// toolHeraMove relocates the caller's live hera binding to a different
// orchestrator (fix-hera-join-move-binding): ends the current binding
// (end_reason "moved") and creates a new worker/freelance role+binding under
// the target orchestrator, in one transaction via db.MoveHeraBinding. Mirrors
// toolHeraJoin's attach-mode validation (coordinator-kind rejection, project
// mirroring, optional initial status) but resolves the SOURCE binding via
// resolveCallerRole (from_orchestrator disambiguates) rather than creating a
// binding from scratch.
func (s *Server) toolHeraMove(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd              string `json:"cwd"`
		Orchestrator     string `json:"orchestrator"`
		RoleName         string `json:"role_name"`
		Kind             string `json:"kind"`
		FromOrchestrator string `json:"from_orchestrator"`
		Status           string `json:"status"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	if p.Orchestrator == "" {
		return toolError(id, "orchestrator is required")
	}
	if p.RoleName == "" {
		return toolError(id, "role_name is required")
	}
	if p.Kind == string(db.HeraKindCoordinator) {
		return toolError(id, "coordinator kind is not accepted here; use hera_new_orchestrator to bootstrap a new orchestrator")
	}
	if p.Kind != string(db.HeraKindWorker) && p.Kind != string(db.HeraKindFreelance) {
		return toolError(id, "kind must be 'worker' or 'freelance'")
	}

	// Resolve the caller's CURRENT (source) binding. from_orchestrator
	// disambiguates when the task holds 2+ live bindings; resolveCallerRole
	// already returns the "nothing to move" (no live binding at all) and
	// "ambiguous" (2+ bindings, no from_orchestrator) error shapes verbatim.
	caller, err := s.resolveCallerRole(p.Cwd, p.FromOrchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	// A coordinator's live binding IS its orchestrator's coordination — ending
	// it (what a move does) orphans the whole subtree the coordinator was
	// running, and the destination kind (worker/freelance) carries no
	// structural link back to that subtree (hera-freelancer-bug: this
	// silently produced two disconnected "freelance" stubs while their
	// original orchestrators were left coordinator-less). There is no
	// agent-facing tool to properly nest an existing coordinator + subtree
	// under a new parent — that is the Hera TUI's `J` adopt/reparent key,
	// human-only — so reject rather than silently orphaning it.
	if caller.role.Kind == db.HeraKindCoordinator {
		return toolError(id, fmt.Sprintf(
			"%q is the live coordinator of orchestrator %q; hera_move would end that coordinator binding and orphan its whole subtree instead of moving it. "+
				"There is no tool for an agent to nest an existing coordinator under a new parent — ask a human to use the Hera TUI's `J` (adopt) key on %q to reparent it under %q",
			caller.role.Name, caller.orch.Name, caller.orch.Name, p.Orchestrator))
	}

	targetOrch, err := s.heraStore.HeraOrchestratorByName(p.Orchestrator)
	if errors.Is(err, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("unknown orchestrator %q", p.Orchestrator))
	}
	if err != nil {
		return toolError(id, fmt.Sprintf("resolve orchestrator: %v", err))
	}

	// Same-orchestrator move is a no-op error: nothing to relocate.
	if targetOrch.ID == caller.orch.ID {
		return toolError(id, fmt.Sprintf(
			"already bound under orchestrator %q; call hera_join(cwd) without role_name to retrieve your current role instead of hera_move",
			p.Orchestrator))
	}

	result, err := s.heraStore.MoveHeraBinding(caller.binding.ID, db.CreateHeraRoleInput{
		OrchestratorID: targetOrch.ID,
		Name:           p.RoleName,
		Kind:           db.HeraRoleKind(p.Kind),
		// M4 fix parity: persist the moving task's argus project on the role.
		ArgusProject: caller.task.Project,
	}, caller.task.ID, caller.task.Worktree)
	if err != nil {
		return toolError(id, fmt.Sprintf("move binding: %v", err))
	}

	// Optional initial status on the new role.
	if p.Status != "" {
		sv := db.HeraRoleStatusValue(p.Status)
		if uErr := s.heraStore.UpsertHeraRoleStatus(result.NewRole.ID, sv); uErr != nil {
			slog.Warn("[hera] upsert initial status failed", "role_id", result.NewRole.ID, "err", uErr)
		}
	}

	// Mirror to task_meta best-effort.
	if metaErr := s.heraStore.SetMeta(caller.task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(result.NewRole.Kind)); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "tool", "hera_move", "task_id", caller.task.ID, "err", metaErr)
	}

	slog.Info("[hera] move ok",
		"from_orch", result.OldOrchestratorName, "from_role", result.OldRoleName,
		"to_orch", targetOrch.Name, "role", result.NewRole.Name, "binding_id", result.NewBinding.ID, "task_id", caller.task.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "Binding moved.\n\n")
	fmt.Fprintf(&b, "- **from_orchestrator**: %s\n", result.OldOrchestratorName)
	fmt.Fprintf(&b, "- **from_role_name**: %s\n", result.OldRoleName)
	fmt.Fprintf(&b, "- **to_orchestrator**: %s\n", targetOrch.Name)
	fmt.Fprintf(&b, "- **role_name**: %s\n", result.NewRole.Name)
	fmt.Fprintf(&b, "- **kind**: %s\n", result.NewRole.Kind)
	fmt.Fprintf(&b, "- **binding_id**: %d\n", result.NewBinding.ID)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", caller.task.ID)
	return toolResult(id, b.String())
}

// toolHeraRebind implements hera_rebind — the supported repair path for a
// hera binding stuck in the claim-says-none / attach-says-exists state
// (BUG-059). A reused worktree_path can leave a live binding pointing at a
// stale argus_task_id, so delivery and status routing (which key on the
// binding's argus_task_id) go nowhere even though the task-then-worktree
// fallback (liveBindingForOrch/liveBindingForTask) already lets a plain claim
// resolve the binding row as-is. hera_rebind handles the harder case where
// the binding row ITSELF needs to change: it reconciles the binding to the
// caller's real live argus task WITHOUT tearing down the argus session — the
// role (and thus its prompt, messages, and status, all keyed on role_id)
// survives; only the binding row is refreshed (end the stale one, insert a
// clean one under the same role).
//
// It refuses rather than guesses whenever the state is genuinely ambiguous:
// two live in_progress tasks share the worktree (surfaced by resolveTask's
// CwdAmbiguousError), multiple roles hold a live binding here and no
// role_name disambiguates, or a DIFFERENT role's live binding already
// occupies the caller's target task or worktree slot.
func (s *Server) toolHeraRebind(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
		RoleName     string `json:"role_name"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "hera_rebind: cwd is required")
	}
	if p.Orchestrator == "" {
		return toolError(id, "hera_rebind: orchestrator is required")
	}

	// The caller's real live task. resolveTask's disambiguation (BUG-059)
	// resolves a shared worktree to the single in_progress task; a genuinely
	// ambiguous cwd (2+ in_progress tasks) surfaces here so this refuses
	// rather than repair against the wrong identity.
	task, err := s.resolveTask("", p.Cwd)
	if err != nil {
		return toolError(id, "hera_rebind: "+err.Error())
	}

	orch, err := s.heraStore.HeraOrchestratorByName(p.Orchestrator)
	if errors.Is(err, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("hera_rebind: orchestrator %q does not exist", p.Orchestrator))
	}
	if err != nil {
		return toolError(id, fmt.Sprintf("hera_rebind: %v", err))
	}

	// Gather the caller's live bindings under this orchestrator, keyed both by
	// the resolved task id and by the worktree path. Under BUG-059 these can
	// disagree; the union (de-duplicated) is the full set of rows in play.
	candidates, err := s.heraRebindCandidates(task, orch.ID)
	if err != nil {
		return toolError(id, fmt.Sprintf("hera_rebind: %v", err))
	}
	if len(candidates) == 0 {
		return toolError(id, fmt.Sprintf(
			"hera_rebind: no live binding to orchestrator %q at this worktree or task; nothing to reconcile. To create a binding, use hera_join with role_name and kind.",
			p.Orchestrator))
	}

	keeperRole, errMsg := s.pickHeraRebindKeeper(orch.ID, candidates, p.RoleName)
	if errMsg != "" {
		return toolError(id, "hera_rebind: "+errMsg)
	}

	// The keeper role's own live binding (role-unique, so at most one).
	var keeperBnd *db.HeraBinding
	if b, bErr := s.heraStore.HeraLiveBindingByRole(keeperRole.ID); bErr == nil {
		keeperBnd = b
	} else if !errors.Is(bErr, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("hera_rebind: load keeper binding: %v", bErr))
	}

	// Who currently occupies the TARGET slots the reconciled binding must own.
	taskOcc, err := heraLiveOrNil(s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orch.ID))
	if err != nil {
		return toolError(id, fmt.Sprintf("hera_rebind: %v", err))
	}
	wtOcc, err := heraLiveOrNil(s.heraStore.HeraLiveBindingByWorktreeAndOrchestrator(task.Worktree, orch.ID))
	if err != nil {
		return toolError(id, fmt.Sprintf("hera_rebind: %v", err))
	}

	// Already consistent: the keeper binding is the sole occupant of both
	// target slots and already points at the caller's task + worktree.
	if keeperBnd != nil &&
		keeperBnd.ArgusTaskID == task.ID &&
		keeperBnd.WorktreePath == task.Worktree &&
		keeperBnd.OrchestratorID == orch.ID &&
		taskOcc != nil && taskOcc.ID == keeperBnd.ID &&
		wtOcc != nil && wtOcc.ID == keeperBnd.ID {
		var b strings.Builder
		fmt.Fprintf(&b, "Binding already consistent; no change needed.\n\n")
		fmt.Fprintf(&b, "- **orchestrator**: %s\n", orch.Name)
		fmt.Fprintf(&b, "- **role_name**: %s\n", keeperRole.Name)
		fmt.Fprintf(&b, "- **kind**: %s\n", keeperRole.Kind)
		fmt.Fprintf(&b, "- **binding_id**: %d\n", keeperBnd.ID)
		fmt.Fprintf(&b, "- **argus_task_id**: %s\n", keeperBnd.ArgusTaskID)
		fmt.Fprintf(&b, "- **reconciled**: false\n")
		return toolResult(id, b.String())
	}

	// Refuse if a DIFFERENT role's live binding holds a target slot — a
	// genuine two-role conflict this verb must not silently resolve.
	if taskOcc != nil && taskOcc.RoleID != keeperRole.ID {
		return toolError(id, fmt.Sprintf(
			"hera_rebind: argus task %s under orchestrator %q is already live-bound to a different role (binding %d); refusing to steal it",
			task.ID, p.Orchestrator, taskOcc.ID))
	}
	if wtOcc != nil && wtOcc.RoleID != keeperRole.ID {
		return toolError(id, fmt.Sprintf(
			"hera_rebind: worktree %q under orchestrator %q is already live-bound to a different role (binding %d); refusing to steal it",
			task.Worktree, p.Orchestrator, wtOcc.ID))
	}

	// Reconcile: end the keeper's stale binding, then insert one clean row
	// pointing at the caller's real task + worktree. Ending + recreating
	// (rather than UPDATE) reuses the DAO's uniqueness enforcement and
	// preserves the role — so messages/status/prompt survive.
	var endedIDs []int64
	if keeperBnd != nil {
		if eErr := s.heraStore.EndHeraBinding(keeperBnd.ID, "hera_rebind"); eErr != nil && !errors.Is(eErr, db.ErrHeraNotFound) {
			return toolError(id, fmt.Sprintf("hera_rebind: end stale binding: %v", eErr))
		}
		endedIDs = append(endedIDs, keeperBnd.ID)
	}

	fresh, err := s.heraStore.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID:         keeperRole.ID,
		OrchestratorID: orch.ID,
		ArgusTaskID:    task.ID,
		WorktreePath:   task.Worktree,
	})
	if err != nil {
		return toolError(id, fmt.Sprintf("hera_rebind: create reconciled binding: %v", err))
	}

	// Mirror role kind to argus task_meta so the rail + auto-adopt see it.
	// Best-effort: a transient failure must not undo the reconcile.
	if metaErr := s.heraStore.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(keeperRole.Kind)); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "tool", "hera_rebind", "task_id", task.ID, "err", metaErr)
	}

	slog.Info("[hera] rebind ok", "orch", orch.Name, "role", keeperRole.Name, "binding_id", fresh.ID, "task_id", task.ID, "ended", endedIDs)
	var b strings.Builder
	fmt.Fprintf(&b, "Binding reconciled to the caller's live argus task.\n\n")
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", orch.Name)
	fmt.Fprintf(&b, "- **role_name**: %s\n", keeperRole.Name)
	fmt.Fprintf(&b, "- **kind**: %s\n", keeperRole.Kind)
	fmt.Fprintf(&b, "- **binding_id**: %d\n", fresh.ID)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", fresh.ArgusTaskID)
	fmt.Fprintf(&b, "- **reconciled**: true\n")
	if len(endedIDs) > 0 {
		fmt.Fprintf(&b, "- **ended_binding_ids**: %v\n", endedIDs)
	}
	return toolResult(id, b.String())
}

// heraRebindCandidates returns the caller's live bindings under orchID, keyed
// by the resolved task id and by the worktree path, de-duplicated by binding
// id — the union is the full set of rows BUG-059 can leave disagreeing.
func (s *Server) heraRebindCandidates(task *model.Task, orchID int64) ([]*db.HeraBinding, error) {
	seen := map[int64]bool{}
	var out []*db.HeraBinding
	add := func(b *db.HeraBinding) {
		if b != nil && !seen[b.ID] {
			seen[b.ID] = true
			out = append(out, b)
		}
	}
	if b, err := s.heraStore.HeraLiveBindingByTaskAndOrchestrator(task.ID, orchID); err == nil {
		add(b)
	} else if !errors.Is(err, db.ErrHeraNotFound) {
		return nil, err
	}
	if task.Worktree != "" {
		if b, err := s.heraStore.HeraLiveBindingByWorktreeAndOrchestrator(task.Worktree, orchID); err == nil {
			add(b)
		} else if !errors.Is(err, db.ErrHeraNotFound) {
			return nil, err
		}
	}
	return out, nil
}

// pickHeraRebindKeeper resolves which role's binding hera_rebind should
// reconcile. With an explicit role_name it must name a role that actually
// holds one of the candidate bindings; without one, exactly one role must be
// represented among the candidates. Any other shape is genuinely ambiguous
// and refused (the returned string is non-empty on refusal).
func (s *Server) pickHeraRebindKeeper(orchID int64, candidates []*db.HeraBinding, roleName string) (*db.HeraRole, string) {
	roleIDs := map[int64]bool{}
	for _, b := range candidates {
		roleIDs[b.RoleID] = true
	}

	if roleName != "" {
		role, err := s.heraStore.HeraRoleByName(orchID, roleName)
		if errors.Is(err, db.ErrHeraNotFound) {
			return nil, fmt.Sprintf("role %q does not exist under this orchestrator", roleName)
		}
		if err != nil {
			return nil, fmt.Sprintf("load role: %v", err)
		}
		if !roleIDs[role.ID] {
			return nil, fmt.Sprintf("role %q has no live binding at this worktree or task; candidates: %s", roleName, s.heraRoleNames(candidates))
		}
		return role, ""
	}

	if len(roleIDs) > 1 {
		return nil, fmt.Sprintf("multiple roles hold live bindings here (%s); pass role_name to pick which to reconcile", s.heraRoleNames(candidates))
	}

	role, err := s.heraStore.HeraRole(candidates[0].RoleID)
	if err != nil {
		return nil, fmt.Sprintf("load role: %v", err)
	}
	return role, ""
}

// heraRoleNames renders a comma-joined list of the candidate bindings' role
// names for ambiguity messages. Unresolvable ids fall back to a "role <id>"
// token rather than erroring the whole message.
func (s *Server) heraRoleNames(candidates []*db.HeraBinding) string {
	names := make([]string, 0, len(candidates))
	for _, b := range candidates {
		name := fmt.Sprintf("role %d", b.RoleID)
		if role, err := s.heraStore.HeraRole(b.RoleID); err == nil {
			name = role.Name
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// heraLiveOrNil folds ErrHeraNotFound into (nil, nil) so call sites can treat
// "no live binding" as a plain nil value instead of a special-cased error.
func heraLiveOrNil(b *db.HeraBinding, err error) (*db.HeraBinding, error) {
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
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
		Status       string `json:"status"`
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

	// D1 (make-hera-plan-living): worker/freelance senders MUST supply a status.
	// Enforce BEFORE recipient resolution so no message is sent on omission.
	switch caller.role.Kind {
	case db.HeraKindWorker, db.HeraKindFreelance:
		if p.Status == "" {
			return toolError(id, "status is required for worker/freelance senders; must be one of idle, working, blocked, done, failed")
		}
	}

	// Validate the status value when supplied (coordinator or worker/freelance).
	var sv db.HeraRoleStatusValue
	if p.Status != "" {
		sv = db.HeraRoleStatusValue(p.Status)
		switch sv {
		case db.HeraStatusIdle, db.HeraStatusWorking, db.HeraStatusBlocked, db.HeraStatusDone, db.HeraStatusFailed:
		default:
			return toolError(id, fmt.Sprintf("invalid status %q; must be one of idle, working, blocked, done, failed", p.Status))
		}
	}

	// Apply status SYNCHRONOUSLY before the message is sent (D1). This decouples
	// the authoritative state change from the best-effort doorbell delivery path.
	// Soft-fail: a status-apply error must NOT block the message send.
	if p.Status != "" {
		if applyErr := s.applyRoleStatus(caller, sv); applyErr != nil {
			slog.Warn("[hera] send: status apply failed (proceeding with send)", "role", caller.role.Name, "status", p.Status, "err", applyErr)
		} else {
			slog.Info("[hera] send: status applied", "role", caller.role.Name, "status", p.Status)
		}
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

// applyRoleStatus validates sv, upserts the role status, mirrors to task_meta,
// and performs the worker task roll (done→in_review+ready_to_close,
// failed→in_review/no-ready_to_close). This is the single shared path used by
// both toolHeraStatus and toolHeraSend — keeping the two callers identical so
// they can never drift (D1, make-hera-plan-living).
//
// caller must be fully resolved before this call. Returns the first hard error
// (upsert failure); the task-roll is always soft-fail (logged, not returned).
// Callers that need soft-fail semantics on the whole apply (hera_send) should
// log any returned error rather than blocking the message send.
func (s *Server) applyRoleStatus(caller *callerRoleResult, sv db.HeraRoleStatusValue) error {
	if err := s.heraStore.UpsertHeraRoleStatus(caller.role.ID, sv); err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}

	// Mirror to task_meta best-effort.
	if metaErr := s.heraStore.SetMeta(caller.binding.ArgusTaskID, db.HeraMetaNamespace, db.HeraMetaKeyThreadStatus, string(sv)); metaErr != nil {
		slog.Warn("[hera] meta mirror failed", "role", caller.role.Name, "task_id", caller.binding.ArgusTaskID, "err", metaErr)
	}

	// BUG-050 PRIMARY trigger: a WORKER reporting status="done" rolls its bound
	// task to in_review + stamps ready_to_close — the idle-but-done case the
	// exit hook misses. Worker-kind ONLY; RollHeraWorkerToReview no-ops unless
	// the task is in_progress (never clobbers human-set state). Soft-fail.
	if sv == db.HeraStatusDone && caller.role.Kind == db.HeraKindWorker {
		if flipped, rErr := s.heraStore.RollHeraWorkerToReview(caller.binding.ArgusTaskID); rErr != nil {
			slog.Warn("[hera] apply-status(done): worker roll failed (status still updated)", "task_id", caller.binding.ArgusTaskID, "err", rErr)
		} else if flipped {
			slog.Info("[hera] apply-status(done): rolled worker task to in_review", "task_id", caller.binding.ArgusTaskID, "role", caller.role.Name)
		}
	}

	// D2 (make-hera-plan-living): a WORKER reporting status="failed" rolls its
	// bound task to in_review WITHOUT stamping ready_to_close. Same soft-fail /
	// idempotent invariants as the done roll above.
	if sv == db.HeraStatusFailed && caller.role.Kind == db.HeraKindWorker {
		if flipped, rErr := s.heraStore.RollHeraWorkerFailed(caller.binding.ArgusTaskID); rErr != nil {
			slog.Warn("[hera] apply-status(failed): worker roll failed (status still updated)", "task_id", caller.binding.ArgusTaskID, "err", rErr)
		} else if flipped {
			slog.Info("[hera] apply-status(failed): rolled worker task to in_review (no ready_to_close)", "task_id", caller.binding.ArgusTaskID, "role", caller.role.Name)
		}
	}

	return nil
}

func (s *Server) toolHeraStatus(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd            string `json:"cwd"`
		Status         string `json:"status"`
		Orchestrator   string `json:"orchestrator"`
		HandoffNote    string `json:"handoff_note"`
		RequestRecycle bool   `json:"request_recycle"`
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
	case db.HeraStatusIdle, db.HeraStatusWorking, db.HeraStatusBlocked, db.HeraStatusDone, db.HeraStatusFailed:
	default:
		return toolError(id, fmt.Sprintf("invalid status %q; must be one of idle, working, blocked, done, failed", p.Status))
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}

	if err := s.applyRoleStatus(caller, sv); err != nil {
		return toolError(id, err.Error())
	}

	// handoff_note / request_recycle (add-coordinator-context-management,
	// widened to any hera-bound role kind by add-worker-bounce): distillate-
	// harvest-before-retire. Both writes are best-effort/soft-fail, matching
	// the status mirror above.
	if p.HandoffNote != "" {
		if metaErr := s.heraStore.SetMeta(caller.binding.ArgusTaskID, db.HeraMetaNamespace, db.HeraMetaKeyHandoffNote, p.HandoffNote); metaErr != nil {
			slog.Warn("[hera] status: handoff_note meta write failed", "role", caller.role.Name, "task_id", caller.binding.ArgusTaskID, "err", metaErr)
		}
	}
	if p.RequestRecycle {
		if metaErr := s.heraStore.SetMeta(caller.binding.ArgusTaskID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "true"); metaErr != nil {
			slog.Warn("[hera] status: request_recycle meta write failed", "role", caller.role.Name, "task_id", caller.binding.ArgusTaskID, "err", metaErr)
		}
	}

	slog.Info("[hera] status ok", "role", caller.role.Name, "status", p.Status, "orch", caller.orch.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "Status updated.\n\n")
	fmt.Fprintf(&b, "- **role**: %s\n", caller.role.Name)
	fmt.Fprintf(&b, "- **status**: %s\n", p.Status)
	fmt.Fprintf(&b, "- **orchestrator**: %s\n", caller.orch.Name)
	if p.HandoffNote != "" {
		fmt.Fprintf(&b, "- **handoff_note**: recorded\n")
	}
	if p.RequestRecycle {
		fmt.Fprintf(&b, "- **request_recycle**: pending\n")
	}
	return toolResult(id, b.String())
}

// toolHeraRevive implements the hera_revive MCP tool (add-hera-revive):
// coordinator-only PULL-revive of one role the caller coordinates. All gating
// logic (dead vs. stuck vs. skip) lives in the shared internal/hera.ReviveRole
// primitive, invoked here via s.heraRevive (wired by the daemon).
func (s *Server) toolHeraRevive(id interface{}, args json.RawMessage) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	if s.heraRevive == nil {
		return toolError(id, "hera revive not configured (daemon did not wire a reviver)")
	}
	var p struct {
		Cwd          string `json:"cwd"`
		RoleName     string `json:"role_name"`
		Orchestrator string `json:"orchestrator"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	roleName := strings.TrimSpace(p.RoleName)
	if roleName == "" {
		return toolError(id, "role_name is required")
	}

	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}
	if caller.role.Kind != db.HeraKindCoordinator {
		return toolError(id, fmt.Sprintf(
			"caller role %q has kind %q; only coordinators may revive a role",
			caller.role.Name, caller.role.Kind))
	}

	target, errResp := s.resolveOrchRole(id, caller.orch.ID, caller.orch.Name, roleName)
	if errResp != nil {
		return errResp
	}
	if target.ID == caller.role.ID {
		return toolError(id, fmt.Sprintf(
			"role %q is your own (live, calling) role; hera_revive targets a DIFFERENT role you coordinate",
			target.Name))
	}

	binding, err := s.heraStore.HeraLiveBindingByRole(target.ID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return toolError(id, fmt.Sprintf("role %q has no live binding (never spawned, or ended)", target.Name))
	}
	if err != nil {
		return toolError(id, fmt.Sprintf("resolve binding for role %q: %v", target.Name, err))
	}

	outcome, err := s.heraRevive(HeraReviveInput{
		TaskID:        binding.ArgusTaskID,
		IsCoordinator: target.Kind == db.HeraKindCoordinator,
	})
	if err != nil {
		return toolError(id, fmt.Sprintf("revive %q: %v", target.Name, err))
	}

	slog.Info("[hera] revive", "orch", caller.orch.Name, "role", target.Name, "task_id", binding.ArgusTaskID, "outcome", outcome)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", heraReviveOutcomeMessage(outcome, target.Name))
	fmt.Fprintf(&b, "- **role**: %s\n", target.Name)
	fmt.Fprintf(&b, "- **argus_task_id**: %s\n", binding.ArgusTaskID)
	fmt.Fprintf(&b, "- **outcome**: %s\n", outcome)
	return toolResult(id, b.String())
}

// heraReviveOutcomeMessage renders a hera.ReviveOutcome (passed as its
// underlying string via HeraReviver, so the mcp package's function signatures
// stay independent of internal/hera's type per the HeraSpawner precedent)
// into a human-readable summary line for the tool response.
func heraReviveOutcomeMessage(outcome, roleName string) string {
	switch outcome {
	case string(hera.ReviveRestartedDead):
		return fmt.Sprintf("%s's session was dead — restarted it in place.", roleName)
	case string(hera.ReviveKickedStuck):
		return fmt.Sprintf("%s was alive but stuck (idle, not blocked) — kicked it to resume.", roleName)
	case string(hera.ReviveSkippedCoordinatorLive):
		return fmt.Sprintf("%s is a live coordinator — never auto-revived; navigate to it or message it directly if it needs attention.", roleName)
	case string(hera.ReviveSkippedBusy):
		return fmt.Sprintf("%s is alive and actively working — left untouched.", roleName)
	case string(hera.ReviveSkippedBlocked):
		return fmt.Sprintf("%s is idle but parked at a question — left untouched to avoid dismissing it.", roleName)
	case string(hera.ReviveSkippedPending):
		return fmt.Sprintf("%s already has a revive/restart in flight — no action taken.", roleName)
	case string(hera.ReviveSkippedNoSessionID):
		return fmt.Sprintf("%s's session is alive but has no session id to resume — left untouched.", roleName)
	default:
		return fmt.Sprintf("%s: outcome %q.", roleName, outcome)
	}
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
		Archetype    string `json:"archetype"`
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
		Archetype:      strings.TrimSpace(p.Archetype),
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
