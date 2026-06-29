package agent

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeraWorkerSpawnInput is the resolved payload for a born-bound hera worker
// spawn. The caller owns name/project/prompt resolution and the orientation
// prefix; this primitive owns ONLY the transactional task + role + binding
// creation. It is shared by every entry point that spawns a hera worker — the
// daemon's MCP arm (hera_spawn_worker) and the native Hera view's rail `w` key —
// so there is exactly one implementation of the LIFO-cleanup spawn semantics.
type HeraWorkerSpawnInput struct {
	OrchestratorID int64  // orchestrator the new worker role + binding belong to
	BaseName       string // base role name; uniquified within the orchestrator
	TaskPrompt     string // orientation-prefixed prompt delivered to the session
	RolePrompt     string // verbatim user prompt, stored on the role row
	Project        string // resolved argus project
	Branch         string // optional base branch
	Backend        string // optional backend override
	Model          string // optional per-worker model override (empty = backend default)
	Archetype      string // optional diligence archetype; defaults to code_slice when empty
	Profile        string // optional per-spawn profile override; empty = use project binding
}

// Default archetypes applied at the spawn layer when the caller supplies none
// (add-diligence-profiles). Both MUST be members of profiles.CanonicalArchetypes:
// a born-bound worker is a code_slice unit, a born-bound coordinator orchestrates.
const (
	defaultWorkerArchetype      = "code_slice"
	defaultCoordinatorArchetype = "orchestrator"
)

// HeraWorkerSpawnResult is the success payload from SpawnHeraWorker.
type HeraWorkerSpawnResult struct {
	Task    *model.Task
	Role    *db.HeraRole
	Binding *db.HeraBinding
}

// SpawnHeraWorker performs the transactional born-bound worker spawn (M4). The
// role + binding write is an AfterPersist hook inside CreateAndStart, so it
// joins that call's LIFO compensating stack: a role/binding-insert failure
// unwinds the task+worktree+row (no orphan task), and a later session-start
// failure unwinds the role+binding too (no orphan role/binding).
// meta:hera.role=worker is stamped inside the hook, before the session starts,
// because the auto-adopt watcher (rule D4) and rail rendering key on it.
//
// This is the single source of truth for hera worker spawn semantics: both the
// daemon's MCP hera_spawn_worker arm and the native Hera view call it, rather
// than each re-deriving the transactional steps.
func SpawnHeraWorker(database *db.DB, runner SessionProvider, in HeraWorkerSpawnInput) (*HeraWorkerSpawnResult, error) {
	// Uniquify the role name within the orchestrator up front so the argus task
	// is titled after the role (not the orientation preamble). The partial
	// unique index on hera_roles is the backstop against a concurrent-spawn race.
	uniqueName, err := database.UniqueHeraRoleName(in.OrchestratorID, in.BaseName)
	if err != nil {
		return nil, err
	}

	// Default an omitted archetype to code_slice (add-diligence-profiles): a
	// born-bound worker is a code-slice unit unless the coordinator says otherwise.
	// The resolved value is both the task's model-resolution key and the role's
	// mirrored display value, so resolve it once here.
	archetype := strings.TrimSpace(in.Archetype)
	if archetype == "" {
		archetype = defaultWorkerArchetype
	}

	var role *db.HeraRole
	var binding *db.HeraBinding
	task, _, err := CreateAndStart(database, runner, CreateInput{
		Name:       uniqueName,
		Prompt:     in.TaskPrompt,
		Project:    in.Project,
		Backend:    in.Backend,
		Model:      in.Model,
		Archetype:  archetype,
		Profile:    in.Profile,
		BaseBranch: in.Branch,
		AutoName:   false, // name is the meaningful role slug — no Haiku rename
		AfterPersist: func(t *model.Task) (func(), error) {
			// Stamp meta:hera.role=worker BEFORE the session starts. Best-effort:
			// a meta failure must not abort an otherwise-valid spawn.
			if mErr := database.SetMeta(t.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)); mErr != nil {
				slog.Warn("[hera] spawn: meta role stamp failed (continuing)", "task", t.ID, "err", mErr)
			}
			r, b, cErr := database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
				OrchestratorID: in.OrchestratorID,
				Name:           uniqueName,
				Kind:           db.HeraKindWorker,
				ArgusProject:   in.Project,
				Prompt:         in.RolePrompt,
				Archetype:      archetype,
			}, t.ID, t.Worktree)
			if cErr != nil {
				// Returning the error makes CreateAndStart unwind the task row +
				// worktree; the session was not started yet, so nothing leaks.
				return nil, cErr
			}
			role, binding = r, b
			// Compensating cleanup for a LATER failure (runner.Start). Deleting
			// the role cascades its binding away, so the subsequent db.Delete
			// (task row) finds no live binding to end — no orphan either way.
			cleanup := func() {
				if dErr := database.DeleteHeraRole(r.ID); dErr != nil {
					slog.Warn("[hera] spawn unwind: delete role failed", "role_id", r.ID, "err", dErr)
				}
			}
			return cleanup, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &HeraWorkerSpawnResult{Task: task, Role: role, Binding: binding}, nil
}

// HeraMaterializeInput is the payload for materializing a PRE-CREATED planned
// role into a live worker (add-hera-plan-substrate). Unlike SpawnHeraWorker
// (which mints a fresh role), this binds and starts the supplied planned role.
// The role's name/project/prompt are already persisted on the row; the gater
// resolves Project and Branch (base_branch from the blocker branches) and the
// check-in-prefixed TaskPrompt before calling.
type HeraMaterializeInput struct {
	Role       *db.HeraRole // the pre-created planned role to bind + start
	TaskPrompt string       // check-in-prefixed prompt delivered to the session
	Project    string       // resolved argus project
	Branch     string       // optional base branch (resolved from blockers)
	Backend    string       // optional backend override
	Model      string       // optional per-worker model override
}

// MaterializeHeraWorker binds and starts a PRE-CREATED planned role, reusing the
// EXACT same agent.CreateAndStart + AfterPersist + LIFO-cleanup machinery as
// born-bound spawn. The ONLY difference from SpawnHeraWorker is that the role
// already exists (created earlier via the plan-authoring tools): instead of
// CreateHeraRoleWithBinding (which inserts a fresh role+binding), the hook inserts
// ONLY the binding (CreateHeraBinding) against in.Role.ID. Materialization is the
// only way a planned node acquires a binding, agent, worktree, and inbox.
//
// The compensating cleanup ends the binding (NOT DeleteHeraRole — the planned
// role is authored data that must survive a failed materialize so the gater can
// retry). A binding-insert failure unwinds the task+worktree; a later
// runner.Start failure ends the binding so no orphan binding is left.
func MaterializeHeraWorker(database *db.DB, runner SessionProvider, in HeraMaterializeInput) (*HeraWorkerSpawnResult, error) {
	if in.Role == nil {
		return nil, fmt.Errorf("materialize: nil role")
	}
	var binding *db.HeraBinding
	task, _, err := CreateAndStart(database, runner, CreateInput{
		Name:    in.Role.Name,
		Prompt:  in.TaskPrompt,
		Project: in.Project,
		Backend: in.Backend,
		Model:   in.Model,
		// The planned role's authored archetype (add-diligence-profiles) propagates
		// onto the materialized task; empty stays empty (resolution falls open).
		Archetype:  in.Role.Archetype,
		BaseBranch: in.Branch,
		AutoName:   false, // name is the planner-assigned short-id slug — never rename
		AfterPersist: func(t *model.Task) (func(), error) {
			if mErr := database.SetMeta(t.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)); mErr != nil {
				slog.Warn("[hera] materialize: meta role stamp failed (continuing)", "task", t.ID, "err", mErr)
			}
			b, cErr := database.CreateHeraBinding(db.CreateHeraBindingInput{
				RoleID:         in.Role.ID,
				OrchestratorID: in.Role.OrchestratorID,
				ArgusTaskID:    t.ID,
				WorktreePath:   t.Worktree,
			})
			if cErr != nil {
				return nil, cErr
			}
			binding = b
			// Compensating cleanup for a LATER failure (runner.Start): END the
			// binding (do NOT delete the role — the planned node is authored data
			// the gater retries against). Ending the binding makes the role a
			// planned node again (no live binding), but ListHeraPlannedNodes keys
			// on "no binding EVER", so a once-materialized role won't re-fire; that
			// is the correct semantics for a failed start (it is held, not retried,
			// matching the never-double-spawn guard).
			cleanup := func() {
				if eErr := database.EndHeraBinding(b.ID, "materialize_failed"); eErr != nil {
					slog.Warn("[hera] materialize unwind: end binding failed", "binding_id", b.ID, "err", eErr)
				}
			}
			return cleanup, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &HeraWorkerSpawnResult{Task: task, Role: in.Role, Binding: binding}, nil
}

// HeraSubCoordMaterializeResult is the success payload from
// MaterializeHeraSubCoordinator (add-hera-subcoord-nodes). The single new task
// holds TWO bindings: ParentBinding (a worker binding against the pre-created
// planned role in the parent orchestrator, occupying its DAG slot) and
// CoordBinding (a coordinator binding in a freshly minted child orchestrator).
// ParentRole is the planned role itself (in.Role); CoordRole is the new "coord"
// role in the child orchestrator.
type HeraSubCoordMaterializeResult struct {
	Task          *model.Task
	ParentRole    *db.HeraRole
	ParentBinding *db.HeraBinding
	ChildOrch     *db.HeraOrchestrator
	CoordRole     *db.HeraRole
	CoordBinding  *db.HeraBinding
}

// MaterializeHeraSubCoordinator materializes a PRE-CREATED planned subcoord node
// (add-hera-subcoord-nodes D2/D3) as a DISTINCT coordinator agent on ONE new task.
// It mirrors MaterializeHeraWorker's "bind the planned role" discipline AND
// SpawnHeraCoordinator's "mint a child orchestrator + coord role" discipline,
// fused onto a single CreateAndStart so both bindings land in one transaction:
//
//	(a) PARENT worker binding against in.Role (CreateHeraBinding) — so the node
//	    keeps its parent-DAG slot and its worker-role status `done` still gates the
//	    parent's dependents, identical to a leaf worker.
//	(b) a NEW child orchestrator (name auto-derived from in.Role.Name, de-collided
//	    via UniqueHeraOrchestratorName) + a coordinator role (defaulted "coord")
//	    bound to the SAME new task.
//
// Because the task holds a worker binding in the parent AND a coordinator binding
// in the child, it nests under the parent via the existing SubtreeOrchIDs
// multi-binding bridge — no new nesting mechanism (D2).
//
// LIFO compensation on a later runner.Start failure: END the parent binding (do
// NOT delete in.Role — it is authored plan data the gater retries against, the
// same rule as MaterializeHeraWorker) AND delete the freshly minted child
// orchestrator (which cascades its coord role + coord binding). The child orch is
// created INSIDE the AfterPersist hook so the hook's compensating cleanup owns its
// teardown, and a hook-internal insert failure unwinds the task+worktree with
// nothing left behind.
func MaterializeHeraSubCoordinator(database *db.DB, runner SessionProvider, in HeraMaterializeInput) (*HeraSubCoordMaterializeResult, error) {
	if in.Role == nil {
		return nil, fmt.Errorf("materialize subcoord: nil role")
	}
	var (
		parentBinding *db.HeraBinding
		childOrch     *db.HeraOrchestrator
		coordRole     *db.HeraRole
		coordBinding  *db.HeraBinding
	)
	task, _, err := CreateAndStart(database, runner, CreateInput{
		Name:    in.Role.Name,
		Prompt:  in.TaskPrompt,
		Project: in.Project,
		Backend: in.Backend,
		Model:   in.Model,
		// The planned subcoord role's authored archetype propagates onto the
		// materialized task (add-diligence-profiles); empty stays empty.
		Archetype:  in.Role.Archetype,
		BaseBranch: in.Branch,
		AutoName:   false, // name is the planner-assigned short-id slug — never rename
		AfterPersist: func(t *model.Task) (func(), error) {
			// The new task is a coordinator (of its own child orchestrator); rail
			// rendering keys on meta:hera.role. Best-effort — a meta failure must not
			// abort an otherwise-valid materialize.
			if mErr := database.SetMeta(t.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)); mErr != nil {
				slog.Warn("[hera] subcoord materialize: meta role stamp failed (continuing)", "task", t.ID, "err", mErr)
			}

			// (a) Parent worker binding against the pre-created planned role.
			pb, cErr := database.CreateHeraBinding(db.CreateHeraBindingInput{
				RoleID:         in.Role.ID,
				OrchestratorID: in.Role.OrchestratorID,
				ArgusTaskID:    t.ID,
				WorktreePath:   t.Worktree,
			})
			if cErr != nil {
				return nil, cErr
			}
			parentBinding = pb

			// (b) Child orchestrator (name derived from the node, de-collided) +
			// coordinator role bound to the SAME task.
			childName, cErr := database.UniqueHeraOrchestratorName(in.Role.Name)
			if cErr != nil {
				return nil, cErr
			}
			// Empty base_branch (add-hera-plan-base-branch): the child orchestrator's
			// own root plan nodes resolve their base via the gater's coordinatorBranch
			// fallback (= this sub-coordinator's bound-task branch). Mirrors
			// SpawnHeraCoordinator, which also passes "".
			co, cErr := database.CreateHeraOrchestrator(childName, "")
			if cErr != nil {
				return nil, cErr
			}
			childOrch = co
			cr, cb, cErr := database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
				OrchestratorID: co.ID,
				Name:           "coord",
				Kind:           db.HeraKindCoordinator,
				ArgusProject:   in.Project,
				Prompt:         in.Role.Prompt,
			}, t.ID, t.Worktree)
			if cErr != nil {
				return nil, cErr
			}
			coordRole, coordBinding = cr, cb

			// Compensating cleanup for a LATER failure (runner.Start). Mirror the
			// proven LIFO discipline:
			//   - END the parent binding (NOT DeleteHeraRole — in.Role is authored
			//     plan data the gater retries against, identical to
			//     MaterializeHeraWorker's rule).
			//   - DELETE the freshly minted child orchestrator, which cascades its
			//     coord role + coord binding away (so no orphan child orch/role/binding).
			cleanup := func() {
				if eErr := database.EndHeraBinding(pb.ID, "subcoord_materialize_failed"); eErr != nil {
					slog.Warn("[hera] subcoord materialize unwind: end parent binding failed", "binding_id", pb.ID, "err", eErr)
				}
				if dErr := database.DeleteHeraOrchestrator(co.ID); dErr != nil {
					slog.Warn("[hera] subcoord materialize unwind: delete child orchestrator failed", "orch_id", co.ID, "err", dErr)
				}
			}
			return cleanup, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &HeraSubCoordMaterializeResult{
		Task:          task,
		ParentRole:    in.Role,
		ParentBinding: parentBinding,
		ChildOrch:     childOrch,
		CoordRole:     coordRole,
		CoordBinding:  coordBinding,
	}, nil
}

// HeraSubCoordinatorOrientation is the orientation prefix delivered to a
// gater-materialized sub-coordinator (add-hera-subcoord-nodes D4). It names the
// child orchestrator it owns and the parent orchestrator it answers to, points at
// the coordination tools (spawn / plan / status / send / inbox), and states the
// core expectation: the sub-coordinator OWNS the decomposition — it runs its own
// brainstorm against the goal and authors its own sub-plan. The full materialized
// prompt is this orientation + HeraCheckInOrientation (check in with the parent,
// poll hera_inbox for go/wait) + the node's goal.
func HeraSubCoordinatorOrientation(childOrchName, parentOrchName, coordRoleName string) string {
	return fmt.Sprintf(
		"You are the coordinator (role %q) of hera sub-orchestrator %q, materialized as a "+
			"sub-team under parent orchestrator %q. You OWN the decomposition of your goal: run "+
			"your own brainstorm against it and author your own sub-plan — your parent handed you a "+
			"goal, not a ready-made plan. Dispatch work with hera_spawn_worker(project=\"...\", "+
			"prompt=\"...\"), structure phases with hera_plan / hera_plan_node, track roles via "+
			"hera_status / hera_inbox / hera_get_messages, and message roles (including your parent "+
			"coordinator) with hera_send. When opening pull requests, use mcp__argus__iris_gh_pr_create "+
			"(not gh pr create directly) so argus records the PR URL and the hera rail shows the PR indicator.",
		coordRoleName, childOrchName, parentOrchName)
}

// HeraCheckInOrientation is the standing-order prefix prepended to a
// gater-materialized worker's prompt. It instructs the worker to FIRST message
// its coordinator that it has started, then POLL hera_inbox in a loop for a
// go/wait decision before doing real work. Worker-PULLED, never a passive push:
// mid-flight pushed doorbells to a busy/fresh worker are unreliable (a known
// hera gotcha), so the worker reads its durable inbox rather than waiting to be
// told. Building this as "wait to be told" would hang the worker on a go that
// silently never arrived.
func HeraCheckInOrientation(orchestrator, coordinator string) string {
	return fmt.Sprintf(
		"You are a worker in hera orchestrator %q under coordinator %q. You were "+
			"materialized automatically because your upstream dependencies finished. "+
			"BEFORE doing any real work: (1) send a brief check-in to your coordinator "+
			"with hera_send (say you have started and are awaiting go/wait); (2) then POLL "+
			"hera_inbox in a loop (re-call it every minute or so) until you READ a 'go' or "+
			"'wait' reply — do NOT sit idle waiting to be messaged, the reply arrives via "+
			"your inbox, not a push. On 'go', proceed; on 'wait', keep polling hera_inbox "+
			"until 'go'. When opening pull requests, use mcp__argus__iris_gh_pr_create.",
		orchestrator, coordinator)
}

// HeraCoordinatorSpawnInput is the resolved payload for a born-bound ROOT
// coordinator spawn (the rail `n` key). It creates a brand-new top-level
// orchestrator, a coordinator role + binding, and the argus task that backs
// them — `hera_new_orchestrator` semantics, but starting from a fresh task. The
// caller owns name/project/prompt resolution; this primitive owns the
// transactional orchestrator + task + role + binding creation with full unwind.
type HeraCoordinatorSpawnInput struct {
	OrchestratorBaseName string // base orchestrator name; de-collided to a fresh active name
	CoordRoleName        string // coordinator role name (defaults to "coord")
	TaskName             string // argus task name (defaults to the unique orchestrator name)
	TaskPrompt           string // orientation-prefixed prompt delivered to the session
	RolePrompt           string // verbatim user prompt, stored on the coordinator role row
	Project              string // resolved argus project
	Branch               string // optional base branch
	Backend              string // optional backend override
	Model                string // optional model override (empty = backend default)
	Archetype            string // optional diligence archetype; defaults to orchestrator when empty
}

// HeraCoordinatorSpawnResult is the success payload from SpawnHeraCoordinator.
type HeraCoordinatorSpawnResult struct {
	Orchestrator *db.HeraOrchestrator
	Task         *model.Task
	Role         *db.HeraRole
	Binding      *db.HeraBinding
}

// SpawnHeraCoordinator creates a NEW top-level orchestrator + coordinator role
// bound to a freshly created argus task (the rail `n` key; BUG-006). It mirrors
// SpawnHeraWorker's transactional discipline:
//   - the orchestrator name is de-collided up front so a genuinely new
//     orchestrator is created (CreateHeraOrchestrator is idempotent by name);
//   - the coordinator role + binding write is an AfterPersist hook inside
//     CreateAndStart, joining its LIFO compensating stack (a role/binding insert
//     failure unwinds the task+worktree; a later session-start failure unwinds
//     the role+binding too);
//   - the orchestrator was created BEFORE CreateAndStart, so any failure removes
//     it explicitly — no orphan empty orchestrator is ever left behind.
//
// meta:hera.role=coordinator is stamped inside the hook (rail rendering keys on
// it), before the session starts. Shared by the native Hera view; the MCP arm
// (hera_new_orchestrator) keeps its own task-resolution path because it binds an
// EXISTING task rather than creating one.
func SpawnHeraCoordinator(database *db.DB, runner SessionProvider, in HeraCoordinatorSpawnInput) (*HeraCoordinatorSpawnResult, error) {
	orchName, err := database.UniqueHeraOrchestratorName(in.OrchestratorBaseName)
	if err != nil {
		return nil, err
	}
	orch, err := database.CreateHeraOrchestrator(orchName, "")
	if err != nil {
		return nil, err
	}
	coordName := in.CoordRoleName
	if coordName == "" {
		coordName = "coord"
	}
	taskName := in.TaskName
	if taskName == "" {
		taskName = orchName
	}

	// Default an omitted archetype to orchestrator (add-diligence-profiles): a
	// born-bound coordinator orchestrates unless told otherwise.
	archetype := strings.TrimSpace(in.Archetype)
	if archetype == "" {
		archetype = defaultCoordinatorArchetype
	}

	var role *db.HeraRole
	var binding *db.HeraBinding
	task, _, err := CreateAndStart(database, runner, CreateInput{
		Name:       taskName,
		Prompt:     in.TaskPrompt,
		Project:    in.Project,
		Backend:    in.Backend,
		Model:      in.Model,
		Archetype:  archetype,
		BaseBranch: in.Branch,
		AutoName:   false, // name is the orchestrator slug — no Haiku rename
		AfterPersist: func(t *model.Task) (func(), error) {
			if mErr := database.SetMeta(t.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)); mErr != nil {
				slog.Warn("[hera] coordinator spawn: meta role stamp failed (continuing)", "task", t.ID, "err", mErr)
			}
			r, b, cErr := database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
				OrchestratorID: orch.ID,
				Name:           coordName,
				Kind:           db.HeraKindCoordinator,
				ArgusProject:   in.Project,
				Prompt:         in.RolePrompt,
				Archetype:      archetype,
			}, t.ID, t.Worktree)
			if cErr != nil {
				return nil, cErr
			}
			role, binding = r, b
			cleanup := func() {
				if dErr := database.DeleteHeraRole(r.ID); dErr != nil {
					slog.Warn("[hera] coordinator spawn unwind: delete role failed", "role_id", r.ID, "err", dErr)
				}
			}
			return cleanup, nil
		},
	})
	if err != nil {
		// The orchestrator was created before CreateAndStart; remove it so a
		// failed spawn never leaks an empty orchestrator. DeleteHeraOrchestrator
		// cascades any leftover role/binding (the LIFO cleanup already removed the
		// role on a start failure, so this is usually a bare-orchestrator delete).
		if dErr := database.DeleteHeraOrchestrator(orch.ID); dErr != nil {
			slog.Warn("[hera] coordinator spawn unwind: delete orchestrator failed", "orch_id", orch.ID, "err", dErr)
		}
		return nil, err
	}
	return &HeraCoordinatorSpawnResult{Orchestrator: orch, Task: task, Role: role, Binding: binding}, nil
}

// HeraCoordinatorOrientation is the orientation prefix prepended to a new root
// coordinator's prompt. It names the orchestrator and points at the coordination
// tools (spawn / status / inbox / send) plus the iris PR convention. Shared by
// the native Hera view's `n` key.
func HeraCoordinatorOrientation(orchestrator string) string {
	return fmt.Sprintf(
		"You are the coordinator of hera orchestrator %q. Dispatch work with "+
			"hera_spawn_worker(project=\"...\", prompt=\"...\"), track roles via hera_status / "+
			"hera_inbox / hera_get_messages, and message roles with hera_send. If you need a "+
			"sub-team in another repo, call hera_new_orchestrator to become a sub-coordinator. "+
			"When opening pull requests, use mcp__argus__iris_gh_pr_create (not gh pr create directly) "+
			"so argus records the PR URL and the hera rail shows the PR indicator.",
		orchestrator)
}

// heraWorkerNameRe matches runs of ASCII lowercase letters and digits, used to
// build a URL-slug-style role name from a prompt.
var heraWorkerNameRe = regexp.MustCompile(`[a-z0-9]+`)

// DeriveHeraWorkerName produces a slug from the first 40 chars of the prompt,
// mirroring Hera's swDeriveWorkerName. Returns "worker" for empty/symbol input.
// Shared by the MCP hera_spawn_worker arm and the native Hera view.
func DeriveHeraWorkerName(prompt string) string {
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

// HeraWorkerOrientation is the orientation prefix prepended to a spawned
// worker's prompt. Ports Hera's spawn-handler guidance verbatim (hera_send for
// progress, sub-coordinator escalation, iris for PRs), augmented to name the
// orchestrator and state that the worker is born-bound. Shared by the MCP arm
// and the native Hera view.
func HeraWorkerOrientation(orchestrator, coordinator string) string {
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
