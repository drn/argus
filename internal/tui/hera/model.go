package hera

import (
	"errors"
	"sort"

	"github.com/drn/argus/internal/db"
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

	// meta:hera.ready_to_close lives in the task-addressed task_meta sidecar.
	// One batch read covers every flagged task; a read error is non-fatal
	// (the flag just won't render).
	heraMeta, _ := r.ListMetaByNamespace(db.HeraMetaNamespace)

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
			rv := buildRoleView(r, role, roleToTask, heraMeta)
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
func buildRoleView(r HeraReader, role *db.HeraRole, roleToTask map[int64]string, heraMeta map[string]map[string]string) RoleView {
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
