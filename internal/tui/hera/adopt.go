package hera

import (
	"errors"
	"fmt"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// adopt.go is the `J` adopt/reparent mutation layer — the native port of the
// plugin's ops.AdoptTaskIntoOrchestrator / ops.ReparentCoordinator. Like ops.go
// it is a THIN adapter over EXISTING M1 store methods on *db.DB; it adds the
// operator-facing targeting, guards, and the BUG-026 teardown, but holds no
// orchestrator/role/binding business logic of its own. The role+binding create
// reuses the SAME DAO calls hera_join's attach-mode and the born-bound spawn
// use (CreateHeraRole + CreateHeraBinding), never a duplicate implementation.

// EndReasonReparented marks a prior parent-link binding ended when a coordinator
// is moved under a new parent (audit trail on the historical row).
const EndReasonReparented = "reparented"

// EndReasonDetached marks a parent-link binding ended when a coordinator is
// detached back to top-level (un-nested) — the same teardown as a re-parent but
// with no new link recreated.
const EndReasonDetached = "detached"

// AdoptStore is the read+write seam the adopt/reparent ops drive. Every method
// is an EXISTING method on *db.DB, which satisfies this implicitly. Remote mode
// has no *db.DB, so the App never constructs AdoptOps there and the `J` key is
// inert (see HeraPage remote-mode guards + App wiring).
type AdoptStore interface {
	// Lookups.
	HeraOrchestrator(id int64) (*db.HeraOrchestrator, error)
	ListHeraOrchestrators(includeArchived bool) ([]*db.HeraOrchestrator, error)
	ListHeraRoles(orchID int64, includeArchived bool) ([]*db.HeraRole, error)
	SubtreeOrchIDs(rootOrchID int64) ([]int64, error)
	HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error)
	ListHeraBindingsByRole(roleID int64) ([]*db.HeraBinding, error)
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	ListHeraBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	UniqueHeraRoleName(orchID int64, base string) (string, error)

	// Mutations. Role+binding creation goes through the TRANSACTIONAL
	// CreateHeraRoleWithBinding (same DAO hera_join attach-mode + born-bound
	// spawn use), so a binding-insert failure (e.g. a worktree-orchestrator
	// uniqueness collision) rolls the freshly-created role back — no orphan role.
	CreateHeraRoleWithBinding(roleIn db.CreateHeraRoleInput, taskID, worktreePath string) (*db.HeraRole, *db.HeraBinding, error)
	EndHeraBinding(bindingID int64, reason string) error
	DeleteHeraRole(id int64) error
	SetMeta(taskID, namespace, key, value string) error
}

// AdoptOps is the `J` adopt/reparent layer over an AdoptStore.
type AdoptOps struct {
	store AdoptStore
}

// NewAdoptOps builds the adopt layer over an M1 store.
func NewAdoptOps(store AdoptStore) *AdoptOps { return &AdoptOps{store: store} }

// AdoptInput describes a freelancer→worker adoption: fold the freelancer's argus
// task into an existing orchestrator as a worker. The App resolves the argus
// task id, orchestrator id (from the picker), a default role name (the
// freelancer's name), and the freelancer's repo + worktree (from the task row).
type AdoptInput struct {
	ArgusTaskID    string
	OrchestratorID int64
	RoleName       string
	ArgusProject   string
	WorktreePath   string
}

// AdoptResult reports what the adoption created.
type AdoptResult struct {
	OrchestratorName string
	RoleName         string
	RoleID           int64
	BindingID        int64
}

// AdoptTaskIntoOrchestrator creates a worker role under the chosen orchestrator
// and a live binding from the freelancer's argus task to that role — the SAME
// role+binding creation hera_join's attach-mode performs.
//
// Native divergence from the plugin's guard: the plugin's freelancers are
// UNMANAGED tasks with zero bindings, so it rejects ANY live binding. Native's
// freelance-kind roles already carry their own live binding under the
// orchestrator they joined, so this rejects only a DUPLICATE binding under the
// SAME chosen orchestrator (the per-(task,orchestrator) unique index), staying
// faithful to the multi-binding model.
//
// The meta:hera.role=worker stamp is best-effort (a transient argus failure must
// not undo the binding), exactly as in attach-mode.
func (o *AdoptOps) AdoptTaskIntoOrchestrator(in AdoptInput) (*AdoptResult, error) {
	taskID := strings.TrimSpace(in.ArgusTaskID)
	if taskID == "" {
		return nil, errors.New("this freelancer has no argus task id to adopt")
	}

	orch, err := o.store.HeraOrchestrator(in.OrchestratorID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("orchestrator %d no longer exists", in.OrchestratorID)
	}
	if err != nil {
		return nil, fmt.Errorf("hera.AdoptTaskIntoOrchestrator: load orchestrator %d: %w", in.OrchestratorID, err)
	}

	// Already-bound-under-this-orchestrator guard: creating a second live binding
	// for the same task under the same orchestrator would violate the
	// per-(task,orchestrator) unique index. Reject with a clear message rather
	// than letting the INSERT fail opaquely.
	if _, err := o.store.HeraLiveBindingByTaskAndOrchestrator(taskID, in.OrchestratorID); err == nil {
		return nil, fmt.Errorf("this task is already bound under %q", orch.Name)
	} else if !errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("hera.AdoptTaskIntoOrchestrator: check existing binding: %w", err)
	}

	name, err := o.uniqueRoleName(in.OrchestratorID, in.RoleName)
	if err != nil {
		return nil, err
	}

	// Transactional create: if the binding insert violates a live-uniqueness
	// index (e.g. the worktree-orchestrator index, which the guard above does
	// NOT pre-check), the role insert is rolled back too — no orphan role.
	role, bnd, err := o.store.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: in.OrchestratorID,
		Name:           name,
		Kind:           db.HeraKindWorker,
		ArgusProject:   strings.TrimSpace(in.ArgusProject),
	}, taskID, strings.TrimSpace(in.WorktreePath))
	if err != nil {
		return nil, fmt.Errorf("hera.AdoptTaskIntoOrchestrator: create role+binding: %w", err)
	}

	// Mirror meta:hera.role=worker. Best-effort: a transient failure must not
	// undo the binding (matches hera_join attach-mode + the born-bound spawn).
	if err := o.store.SetMeta(taskID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)); err != nil {
		uxlog.Log("[hera-view] adopt: best-effort SetMeta failed for task %s: %v", taskID, err)
	}

	uxlog.Log("[hera-view] adopt: task=%s → role %d (%s) orch=%d (%s)", taskID, role.ID, role.Name, orch.ID, orch.Name)
	return &AdoptResult{
		OrchestratorName: orch.Name,
		RoleName:         role.Name,
		RoleID:           role.ID,
		BindingID:        bnd.ID,
	}, nil
}

// ReparentInput describes a coordinator re-parent: nest coordinator C (its own
// orchestrator) under another coordinator P as a sub-coordinator. The App
// supplies the child orchestrator id and the chosen parent orchestrator id; C's
// coordinator argus task + worktree are resolved from C's coordinator role's
// latest binding.
type ReparentInput struct {
	// ChildOrchestratorID is the orchestrator being re-parented (C).
	ChildOrchestratorID int64
	// CoordTaskID is an OPTIONAL hint (carried from the rail selection when the
	// coord session is live). It is unused for resolution — the op always reads
	// the latest coord binding — but kept for symmetry with the plugin/logging.
	CoordTaskID string
	// ParentOrchestratorID is the chosen new parent (P).
	ParentOrchestratorID int64
	// RoleName defaults to C's name; de-collided against P's active roles.
	RoleName string
	// ArgusProject is C's repo, recorded on the new worker role. Optional.
	ArgusProject string
}

// ReparentResult reports what the re-parent created.
type ReparentResult struct {
	ParentOrchestratorName string
	ChildOrchestratorName  string
	RoleName               string
	RoleID                 int64
	BindingID              int64
}

// ReparentCoordinator nests coordinator C (ChildOrchestratorID) under parent P
// (ParentOrchestratorID) by creating a worker role under P bound to C's
// coordinator argus task — the multi-binding the orchestration tree renders as a
// nested sub-coordinator. If C is already nested under some other parent, EVERY
// prior parent linkage is torn down first so the move is clean (BUG-026).
//
// Guards:
//   - P must exist and differ from C (a coordinator cannot adopt itself).
//   - P must not be C or a descendant of C (SubtreeOrchIDs(C) includes C and its
//     whole subtree) — nesting C under its own descendant would create a cycle.
//   - C must have a coordinator role with at least one binding (live OR ended);
//     re-parenting links to C's coordinator argus TASK, which outlives its
//     session, so the task id + worktree resolve from the coord role's LATEST
//     binding. A coordinator whose coord role never had a binding cannot move.
func (o *AdoptOps) ReparentCoordinator(in ReparentInput) (*ReparentResult, error) {
	if in.ParentOrchestratorID == in.ChildOrchestratorID {
		return nil, errors.New("a coordinator cannot be adopted under itself")
	}

	child, err := o.store.HeraOrchestrator(in.ChildOrchestratorID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("coordinator %d no longer exists", in.ChildOrchestratorID)
	}
	if err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: load child %d: %w", in.ChildOrchestratorID, err)
	}

	parent, err := o.store.HeraOrchestrator(in.ParentOrchestratorID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("coordinator %d no longer exists", in.ParentOrchestratorID)
	}
	if err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: load parent %d: %w", in.ParentOrchestratorID, err)
	}

	// Cycle guard: the chosen parent must not be the child or any of the child's
	// descendants. SubtreeOrchIDs(child) includes child itself, so this also
	// catches a direct self-target.
	subtree, err := o.store.SubtreeOrchIDs(in.ChildOrchestratorID)
	if err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: subtree of %d: %w", in.ChildOrchestratorID, err)
	}
	for _, id := range subtree {
		if id == in.ParentOrchestratorID {
			return nil, fmt.Errorf(
				"cannot adopt %q under %q — %q is one of %q's own sub-coordinators (would create a cycle)",
				child.Name, parent.Name, parent.Name, child.Name)
		}
	}

	// Resolve C's coordinator argus task id + worktree from the coordinator
	// role's LATEST binding (live, else most-recent ended) so a dormant
	// coordinator re-parents too.
	coordRole, err := o.coordRoleOf(in.ChildOrchestratorID, child.Name)
	if err != nil {
		return nil, err
	}
	hist, err := o.store.ListHeraBindingsByRole(coordRole.ID)
	if err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: bindings for coord role %d: %w", coordRole.ID, err)
	}
	if len(hist) == 0 {
		return nil, fmt.Errorf("%q has never had a coordinator binding to re-parent", child.Name)
	}
	latest := hist[0] // newest-first
	taskID := strings.TrimSpace(latest.ArgusTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("%q has no argus task id to re-parent", child.Name)
	}
	coordWorktree := latest.WorktreePath
	if coordWorktree == "" {
		return nil, fmt.Errorf("%q has no coordinator worktree to re-parent", child.Name)
	}

	// BUG-026 teardown: end EVERY prior parent linkage of C's coord task by ROLE
	// id so the re-parent is IDEMPOTENT — repeated J never piles up de-collided
	// duplicate link roles. The single clean link is recreated below; the
	// teardown frees up the name for it. Shared single-source with DetachCoordinator.
	if _, err := o.teardownParentLinks(taskID, coordRole.ID, EndReasonReparented); err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: %w", err)
	}

	name, err := o.uniqueRoleName(in.ParentOrchestratorID, defaultStr(in.RoleName, child.Name))
	if err != nil {
		return nil, err
	}

	// Transactional create (same rationale as adopt) — a binding-insert failure
	// rolls the new link role back rather than leaving an orphan.
	role, bnd, err := o.store.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: in.ParentOrchestratorID,
		Name:           name,
		Kind:           db.HeraKindWorker,
		ArgusProject:   strings.TrimSpace(in.ArgusProject),
	}, taskID, coordWorktree)
	if err != nil {
		return nil, fmt.Errorf("hera.ReparentCoordinator: create role+binding: %w", err)
	}

	uxlog.Log("[hera-view] reparent: child=%q (task=%s) under parent=%q → role %d (%s)",
		child.Name, taskID, parent.Name, role.ID, role.Name)
	return &ReparentResult{
		ParentOrchestratorName: parent.Name,
		ChildOrchestratorName:  child.Name,
		RoleName:               role.Name,
		RoleID:                 role.ID,
		BindingID:              bnd.ID,
	}, nil
}

// DetachResult reports the outcome of a detach-to-top-level.
type DetachResult struct {
	// ChildOrchestratorName is the detached coordinator's orchestrator name.
	ChildOrchestratorName string
	// LinksRemoved is the number of distinct parent-link roles torn down. Zero
	// means the coordinator was already top-level (an idempotent clean no-op).
	LinksRemoved int
}

// DetachCoordinator un-nests coordinator C (childOrchestratorID) back to a
// top-level root orchestrator with no parent, WITHOUT creating any new link. It
// is exactly ReparentCoordinator's BUG-026 teardown with no recreate: resolve
// C's coord role + argus task from the coord role's LATEST binding (live, else
// most-recent ended — same as re-parent), then run the shared teardown to end
// every live parent-link binding and delete every distinct parent-link role
// (all bindings of C's coord task on roles OTHER than C's own coordinator role).
// C's own coordinator role + binding are never touched, so C and its whole
// subtree survive intact at top-level.
//
// Idempotent: a coordinator that is already top-level (no parent-link roles) is
// a clean no-op (LinksRemoved == 0, no error). Rejected only when the
// orchestrator no longer exists, or C has no coordinator role / no binding to
// resolve its task.
func (o *AdoptOps) DetachCoordinator(childOrchestratorID int64) (*DetachResult, error) {
	child, err := o.store.HeraOrchestrator(childOrchestratorID)
	if errors.Is(err, db.ErrHeraNotFound) {
		return nil, fmt.Errorf("coordinator %d no longer exists", childOrchestratorID)
	}
	if err != nil {
		return nil, fmt.Errorf("hera.DetachCoordinator: load child %d: %w", childOrchestratorID, err)
	}

	// Resolve C's coordinator argus task id from the coord role's LATEST binding
	// (live, else most-recent ended) so a dormant coordinator detaches too —
	// identical resolution to ReparentCoordinator.
	coordRole, err := o.coordRoleOf(childOrchestratorID, child.Name)
	if err != nil {
		return nil, err
	}
	hist, err := o.store.ListHeraBindingsByRole(coordRole.ID)
	if err != nil {
		return nil, fmt.Errorf("hera.DetachCoordinator: bindings for coord role %d: %w", coordRole.ID, err)
	}
	if len(hist) == 0 {
		return nil, fmt.Errorf("%q has never had a coordinator binding to detach", child.Name)
	}
	taskID := strings.TrimSpace(hist[0].ArgusTaskID) // newest-first
	if taskID == "" {
		return nil, fmt.Errorf("%q has no argus task id to detach", child.Name)
	}

	removed, err := o.teardownParentLinks(taskID, coordRole.ID, EndReasonDetached)
	if err != nil {
		return nil, fmt.Errorf("hera.DetachCoordinator: %w", err)
	}
	if removed == 0 {
		uxlog.Log("[hera-view] detach: %q (task=%s) already top-level — no-op", child.Name, taskID)
	} else {
		uxlog.Log("[hera-view] detach: %q (task=%s) → top-level, removed %d parent link(s)", child.Name, taskID, removed)
	}
	return &DetachResult{ChildOrchestratorName: child.Name, LinksRemoved: removed}, nil
}

// teardownParentLinks ends every LIVE parent-link binding of coordTaskID and
// deletes every distinct parent-link role, EXCLUDING C's own coordinator role
// (coordRoleID). A "parent link" is any binding of C's coord task on a role
// other than C's own coordinator role — i.e. a bridge binding some parent
// orchestrator holds over C. Live bindings are ended (audit reason) BEFORE the
// role delete cascades them; then every distinct link role (live OR ended) is
// deleted so its bindings cascade away. Returns the count of distinct
// parent-link roles deleted (0 ⇒ C was already top-level). Single-source
// teardown shared by ReparentCoordinator (clean slate before recreating the one
// new link) and DetachCoordinator (teardown with no recreate).
func (o *AdoptOps) teardownParentLinks(taskID string, coordRoleID int64, reason string) (int, error) {
	liveLinks, err := o.store.ListHeraLiveBindingsByTask(taskID)
	if err != nil {
		return 0, fmt.Errorf("live bindings for %s: %w", taskID, err)
	}
	for _, bnd := range liveLinks {
		if bnd.RoleID == coordRoleID {
			continue // C's own coordinator binding — never a parent link.
		}
		if err := o.store.EndHeraBinding(bnd.ID, reason); err != nil {
			return 0, fmt.Errorf("end prior parent binding %d: %w", bnd.ID, err)
		}
	}
	allLinks, err := o.store.ListHeraBindingsByTask(taskID)
	if err != nil {
		return 0, fmt.Errorf("all bindings for %s: %w", taskID, err)
	}
	deleted := make(map[int64]bool)
	for _, bnd := range allLinks {
		if bnd.RoleID == 0 || bnd.RoleID == coordRoleID || deleted[bnd.RoleID] {
			continue
		}
		deleted[bnd.RoleID] = true
		if err := o.store.DeleteHeraRole(bnd.RoleID); err != nil && !errors.Is(err, db.ErrHeraNotFound) {
			return 0, fmt.Errorf("delete prior parent role %d: %w", bnd.RoleID, err)
		}
	}
	return len(deleted), nil
}

// ListActiveOrchestrators returns the active (non-archived) orchestrators for
// the `J` picker, in the store's listing order.
func (o *AdoptOps) ListActiveOrchestrators() ([]*db.HeraOrchestrator, error) {
	orchs, err := o.store.ListHeraOrchestrators(false)
	if err != nil {
		return nil, fmt.Errorf("hera.ListActiveOrchestrators: %w", err)
	}
	return orchs, nil
}

// coordRoleOf returns the first coordinator role under orchID (active or
// archived), or an error when the orchestrator has no coordinator role at all.
// Shared by ReparentCoordinator and DetachCoordinator, so the wording is generic.
func (o *AdoptOps) coordRoleOf(orchID int64, name string) (*db.HeraRole, error) {
	roles, err := o.store.ListHeraRoles(orchID, true)
	if err != nil {
		return nil, fmt.Errorf("hera: list roles for %d: %w", orchID, err)
	}
	for _, r := range roles {
		if r.Kind == db.HeraKindCoordinator {
			return r, nil
		}
	}
	return nil, fmt.Errorf("%q has no coordinator role", name)
}

// uniqueRoleName de-collides the requested role name against the orchestrator's
// active roles via the store (delegates to db.UniqueHeraRoleName).
func (o *AdoptOps) uniqueRoleName(orchID int64, requested string) (string, error) {
	name, err := o.store.UniqueHeraRoleName(orchID, strings.TrimSpace(requested))
	if err != nil {
		return "", fmt.Errorf("hera: de-collide role name: %w", err)
	}
	return name, nil
}

// defaultStr returns s trimmed, or fallback when s is blank.
func defaultStr(s, fallback string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return fallback
}
