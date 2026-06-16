package hera

import (
	"errors"
	"sort"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// RoleView is the read-only render projection of one hera role plus the live
// state the rail needs: its status (idle/working/blocked/done), the argus task
// its live binding points at (empty when unbound), and whether that task is
// flagged meta:hera.ready_to_close (M4 — a finished worker awaiting close-out).
type RoleView struct {
	RoleID       int64
	OrchID       int64
	Name         string
	Kind         db.HeraRoleKind
	Status       db.HeraRoleStatusValue // "" when no status row exists
	HasStatus    bool
	TaskID       string // live binding's argus task, or "" when unbound
	Live         bool   // has a live binding
	ReadyToClose bool   // bound task carries meta:hera.ready_to_close=true
	Archived     bool   // role archived_at set
	// BridgeTaskID is the role's LATEST binding argus task regardless of liveness
	// (== TaskID when Live). It is the STRUCTURAL nesting key: a worker bridges a
	// child orchestrator when its BridgeTaskID equals that child's coordinator's
	// BridgeTaskID, even after the binding ended. "" when the role never bound.
	BridgeTaskID string
	// LinkEndReason carries the latest binding's end_reason when the role is NOT
	// live ("" when live). A bridge is honoured unless this is an operator
	// teardown (reparented / user_deleted) — see db.HeraEndReasonIsTeardown.
	LinkEndReason string
	// TaskStatus / TaskResult are the bound argus task's workflow status
	// ("in_progress"/"complete"/…) and opaque result JSON. They feed the
	// orchestration-tree DAG's node colour + failed glyph (the rail's own status
	// icons use the hera role Status above, not these). Empty when unbound.
	TaskStatus string
	TaskResult string
}

// OrchView is the render projection of one orchestrator and its non-freelance
// roles (coordinator + worker). Freelance-kind roles are hoisted into the
// Model's Freelance section rather than nested here.
type OrchView struct {
	ID       int64
	Name     string
	Pinned   bool
	Archived bool
	Roles    []RoleView
}

// Model is the full read-only snapshot the rail renders. Orchestrators are
// partitioned into the rail's sections; Freelance aggregates freelance-kind
// roles across all active orchestrators.
//
// Multi-binding fan-out is structural and automatic: a single argus task bound
// under two orchestrators is reached through two DISTINCT roles (one per
// orchestrator), so it surfaces once under EACH orchestrator. No special case
// is needed — the per-orchestrator role walk produces the fan-out the locked
// design (Q2/Q3) and the smoke test require.
type Model struct {
	Pinned    []OrchView // pinned orchestrators (pinned_at set)
	Active    []OrchView // active, non-pinned orchestrators
	Archived  []OrchView // archived orchestrators (archived_at set)
	Freelance []RoleView // freelance-kind roles across active orchestrators
}

// IsEmpty reports whether the snapshot has no content at all (used to render
// the empty-state placeholder).
func (m Model) IsEmpty() bool {
	return len(m.Pinned) == 0 && len(m.Active) == 0 &&
		len(m.Archived) == 0 && len(m.Freelance) == 0
}

// OrchByID finds the OrchView with the given id across every non-freelance
// section, returning a pointer into the model's backing array (so callers read
// the live projection, never a copy), or nil when not found. Used to resolve a
// selected role's containing orchestrator — the disambiguator that makes a
// multi-binding task's two roles feed two different pane contexts.
func (m *Model) OrchByID(id int64) *OrchView {
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if sec[i].ID == id {
				return &sec[i]
			}
		}
	}
	return nil
}

// CoordTaskID returns the live argus task of this orchestrator's coordinator
// role, or "" when no coordinator role holds a live binding. The HERA pane
// feeds from this so it always shows the orchestrator's coordinator session,
// regardless of which role under the orchestrator is selected (the coord-vs-
// agent session rule — see panes.go).
func (o *OrchView) CoordTaskID() string {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator && o.Roles[i].Live {
			return o.Roles[i].TaskID
		}
	}
	return ""
}

// CoordBridgeTaskID returns this orchestrator's coordinator role's STRUCTURAL
// bridge task (its latest binding regardless of liveness), or "" when no
// coordinator role ever bound. Unlike CoordTaskID (live-only, gated so the COORD
// pane never binds a tombstone), this survives a dormant/finished coordinator so
// a sub-orchestrator still nests under its parent after the coordinator's task
// completed (the bridging-breadth rule). First coordinator role wins.
func (o *OrchView) CoordBridgeTaskID() string {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator {
			if k := bridgeTaskID(&o.Roles[i]); k != "" {
				return k
			}
		}
	}
	return ""
}

// bridgeTaskID returns a role's structural bridge key: its latest-binding task
// (BridgeTaskID), falling back to the live TaskID when the model did not
// populate the bridge field (older callers / hand-built test fixtures). In
// production BuildModel always sets BridgeTaskID, so the fallback only matters
// for fixtures that set TaskID alone.
func bridgeTaskID(r *RoleView) string {
	if r.BridgeTaskID != "" {
		return r.BridgeTaskID
	}
	return r.TaskID
}

// roleBridges reports whether a role's parent link is structurally intact for
// nesting: it bridges when live, or when its latest binding ended for a
// non-teardown reason. An operator-teardown link (reparented / user_deleted) is
// stale and must not nest its child.
func roleBridges(r *RoleView) bool {
	return r.Live || !db.HeraEndReasonIsTeardown(r.LinkEndReason)
}

// CoordRole returns this orchestrator's coordinator role, or nil. Used by the
// rail header (which folds the coordinator into itself) to read its status glyph.
func (o *OrchView) CoordRole() *RoleView {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator {
			return &o.Roles[i]
		}
	}
	return nil
}

// Selection is the (role, orchestrator, task) context resolved from the rail
// cursor. It is the single value threaded to the pane feeds (6b) and — via
// HeraPage.SelectionContext — to the future mutation extension point (6c). The
// orchestrator is ALWAYS the disambiguator: a multi-binding task reached
// through two different roles yields two different Selections (different Role,
// different Orch), so every downstream op acts on the right binding's task.
type Selection struct {
	Role *RoleView // selected role; nil when the cursor is on an orch header
	Orch *OrchView // selected/containing orchestrator; nil when the rail is empty
}

// TaskID returns the selected role's bound argus task, or "" when none.
func (s Selection) TaskID() string {
	if s.Role == nil {
		return ""
	}
	return s.Role.TaskID
}

// FocusTaskID returns the argus task the selection's pane/reattach acts on: the
// selected role's bound task, or — for a coordinator header with no role row
// (the folded coordinator) — the orchestrator's coordinator task. "" when
// neither resolves.
func (s Selection) FocusTaskID() string {
	if t := s.TaskID(); t != "" {
		return t
	}
	if s.IsCoordinator() {
		return s.CoordTaskID()
	}
	return ""
}

// IsCoordinator reports whether the selection represents a coordinator. The
// right region renders the coordinator details summary (not a terminal) for a
// coordinator selection. Since the coordinator role is folded into the
// orchestrator HEADER (no separate child row), a header selection (Role nil,
// Orch set) IS a coordinator selection; an explicit coordinator-kind role still
// counts too (defensive — coordinators no longer render as their own rows).
func (s Selection) IsCoordinator() bool {
	if s.Role != nil {
		return s.Role.Kind == db.HeraKindCoordinator
	}
	return s.Orch != nil
}

// CoordTaskID returns the live coordinator task of the selected orchestrator,
// or "" when none. The HERA (middle) pane feeds from this.
func (s Selection) CoordTaskID() string {
	if s.Orch == nil {
		return ""
	}
	return s.Orch.CoordTaskID()
}

// BuildModel reads the hera store and assembles the read-only rail snapshot.
// It is pure-read: every call goes through HeraReader's List/Status/Meta
// methods, all of which are mutex-guarded and fast on *db.DB, so it is safe to
// call on the tview thread.
//
// Soft-fail discipline: a per-role status lookup that returns ErrHeraNotFound
// is normal (no status row yet) and leaves Status zero. Any other read error
// aborts and is returned so the caller can log it and keep the prior model.
func BuildModel(r HeraReader) (Model, error) {
	var m Model
	if r == nil {
		return m, nil
	}

	orchs, err := r.ListHeraOrchestrators(true) // include archived
	if err != nil {
		return Model{}, err
	}

	bindings, err := r.ListHeraLiveBindings()
	if err != nil {
		return Model{}, err
	}
	roleToTask := make(map[int64]string, len(bindings))
	for _, b := range bindings {
		roleToTask[b.RoleID] = b.ArgusTaskID
	}

	// Latest binding per role (live OR ended) drives the structural rail bridge:
	// a role's BridgeTaskID/LinkEndReason come from here so an ended-but-not-
	// torn-down link still nests its child. A read error is non-fatal — bridging
	// just falls back to live-binding behaviour (BridgeTaskID == live TaskID).
	roleToLatest := make(map[int64]*db.HeraBinding)
	if latest, lerr := r.ListHeraLatestBindings(); lerr == nil {
		for _, b := range latest {
			roleToLatest[b.RoleID] = b
		}
	}

	// meta:hera.ready_to_close lives in the task-addressed task_meta sidecar.
	// One batch read covers every flagged task; a read error is non-fatal
	// (the flag just won't render).
	heraMeta, _ := r.ListMetaByNamespace(db.HeraMetaNamespace)

	// Task snapshot keyed by ID so each bound role can carry its argus task's
	// status + result (the orchestration-tree DAG colours nodes by task
	// progress). A read error is non-fatal — nodes just render uncoloured.
	taskByID := make(map[string]*model.Task)
	if tasks, terr := r.Tasks(); terr == nil {
		for _, t := range tasks {
			taskByID[t.ID] = t
		}
	}

	for _, o := range orchs {
		ov := OrchView{
			ID:       o.ID,
			Name:     o.Name,
			Pinned:   o.PinnedAt != nil,
			Archived: o.ArchivedAt != nil,
		}
		roles, err := r.ListHeraRoles(o.ID, true) // include archived roles
		if err != nil {
			return Model{}, err
		}
		for _, role := range roles {
			rv := buildRoleView(r, role, roleToTask, roleToLatest, heraMeta, taskByID)
			if role.Kind == db.HeraKindFreelance && role.ArchivedAt == nil && o.ArchivedAt == nil {
				// Active freelance roles live in their own top-level section.
				m.Freelance = append(m.Freelance, rv)
				continue
			}
			ov.Roles = append(ov.Roles, rv)
		}
		switch {
		case ov.Archived:
			m.Archived = append(m.Archived, ov)
		case ov.Pinned:
			m.Pinned = append(m.Pinned, ov)
		default:
			m.Active = append(m.Active, ov)
		}
	}

	sort.SliceStable(m.Freelance, func(i, j int) bool {
		return m.Freelance[i].Name < m.Freelance[j].Name
	})
	return m, nil
}

// buildRoleView projects one db.HeraRole into a RoleView, resolving its live
// binding's task, status row, and ready_to_close flag.
func buildRoleView(r HeraReader, role *db.HeraRole, roleToTask map[int64]string, roleToLatest map[int64]*db.HeraBinding, heraMeta map[string]map[string]string, taskByID map[string]*model.Task) RoleView {
	rv := RoleView{
		RoleID:   role.ID,
		OrchID:   role.OrchestratorID,
		Name:     role.Name,
		Kind:     role.Kind,
		Archived: role.ArchivedAt != nil,
	}
	if taskID, ok := roleToTask[role.ID]; ok {
		rv.TaskID = taskID
		rv.Live = true
		if kv := heraMeta[taskID]; kv != nil && kv[db.HeraMetaKeyReadyToClose] == "true" {
			rv.ReadyToClose = true
		}
		if t := taskByID[taskID]; t != nil {
			rv.TaskStatus = t.Status.String()
			rv.TaskResult = t.Result
		}
	}
	// Structural bridge key: the role's LATEST binding regardless of liveness.
	// For a live role this is the same live task (empty end_reason); for a
	// finished role it is the most-recent ended binding's task + its end_reason,
	// so the rail can still nest a child whose link ended for a non-teardown
	// reason. Fall back to the live task when the latest-binding read was empty.
	if b := roleToLatest[role.ID]; b != nil {
		rv.BridgeTaskID = b.ArgusTaskID
		if b.EndedAt != nil {
			rv.LinkEndReason = b.EndReason
		}
	} else {
		rv.BridgeTaskID = rv.TaskID
	}
	if st, err := r.HeraRoleStatusFor(role.ID); err == nil {
		rv.Status = st.Status
		rv.HasStatus = true
	} else if !errors.Is(err, db.ErrHeraNotFound) {
		// A non-"missing" status error is unusual; leave status zero rather
		// than aborting the whole rebuild for one role.
		rv.HasStatus = false
	}
	return rv
}
