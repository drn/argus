package hera

import "github.com/drn/argus/internal/db"

// eol.go holds the PURE, testable selection helpers behind the rail's
// end-of-life (EOL) keys: `C` (prune a coordinator's archived descendants) and
// `Ctrl+R` (rail-wide prune of finished roles). They are reads over the rail's
// already-built Model — no I/O, no mutation — so the App can call them on the
// tview main thread and the reclaim/delete side effects live in heraactions.go.

// IsFinished reports whether a role has reached an end state and is a candidate
// for the rail-wide prune (`Ctrl+R`). A role is finished when it is archived,
// when its hera status is `done`, or when its bound task is flagged
// ready_to_close. A live, in-progress role is never finished.
func (r *RoleView) IsFinished() bool {
	if r.Archived {
		return true
	}
	if r.HasStatus && r.Status == db.HeraStatusDone {
		return true
	}
	return r.ReadyToClose
}

// SubtreeArchivedWorkers returns the ARCHIVED worker roles in the orchestration
// subtree rooted at orchID (the selected coordinator, inclusive of nested
// sub-teams via BridgeSubtree), as value copies so a later rail rebuild can't
// alias them. Coordinator and freelance roles are excluded — `C` retires
// archived WORKERS only. Empty when orchID is unknown or has no archived
// descendant workers.
func (m Model) SubtreeArchivedWorkers(orchID int64) []RoleView {
	var out []RoleView
	for _, o := range m.BridgeSubtree(orchID) {
		for i := range o.Roles {
			r := o.Roles[i]
			if r.Kind == db.HeraKindWorker && r.Archived {
				out = append(out, r)
			}
		}
	}
	return out
}

// FinishedRoles gathers every finished role across the whole rail (managed roles
// under Pinned/Active/Archived orchestrators plus hoisted Freelance roles), as
// value copies. It backs the `Ctrl+R` rail-wide prune.
func (m Model) FinishedRoles() []RoleView {
	var out []RoleView
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			for j := range sec[i].Roles {
				if r := sec[i].Roles[j]; r.IsFinished() {
					out = append(out, r)
				}
			}
		}
	}
	for i := range m.Freelance {
		if r := m.Freelance[i]; r.IsFinished() {
			out = append(out, r)
		}
	}
	return out
}

// FullyFinishedOrchestratorIDs returns the ids of orchestrators whose every role
// is finished (and which have at least one role). After `Ctrl+R` reclaims each
// finished role, these orchestrators are deleted so a closed-out coordinator's
// header doesn't linger empty on the rail. An orchestrator with no roles is not
// reported (nothing to close).
func (m Model) FullyFinishedOrchestratorIDs() []int64 {
	var out []int64
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			o := &sec[i]
			if len(o.Roles) == 0 {
				continue
			}
			all := true
			for j := range o.Roles {
				if !o.Roles[j].IsFinished() {
					all = false
					break
				}
			}
			if all {
				out = append(out, o.ID)
			}
		}
	}
	return out
}
