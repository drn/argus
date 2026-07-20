package hera

import "github.com/drn/argus/internal/db"

// eol.go holds the PURE, testable selection helper behind the rail's `C`
// (clear-this-coordinator's-archive) key: a read over the rail's already-built
// Model — no I/O, no mutation — so the App can call it on the tview main thread
// and the reclaim/nuke side effects live in heraactions.go. (BUG-022 removed the
// rail-wide `Ctrl+R` prune, so the finished-role helpers it needed are gone.)

// SubtreeArchivedWorkers returns the ARCHIVED (Tier-1 hidden) worker roles in the
// orchestration subtree rooted at orchID (the selected coordinator, inclusive of
// nested sub-teams via BridgeSubtree), as value copies so a later rail rebuild
// can't alias them. Coordinator and freelance roles are excluded — `C` nukes the
// coordinator's hidden WORKERS only. This INCLUDES an archived worker role that
// itself bridges a nested sub-coordinator (ending that role's own binding is
// correct and desired) — see SubtreeArchivedBridges, which `C` additionally
// cascade-deletes the bridged child orchestrator's whole subtree for: without
// that cascade, the child orchestrator's own row keeps no parent link once this
// binding ends, and it would reappear as a new top-level root on the next rail
// rebuild (the bug SubtreeArchivedBridges closes). Empty when orchID is unknown
// or has no hidden descendant workers. (Nuked roles never appear in the Model,
// so this only ever returns Tier-1 hidden roles.)
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

// SubtreeArchivedBridges returns the child orchestrator IDs of every ARCHIVED
// (Tier-1 hidden) bridging worker role in the subtree rooted at orchID — a
// nested sub-coordinator whose parent-side row was hidden. `C` must fully
// cascade-delete each of these (the whole nested sub-team, at any depth) in
// ADDITION to ending the bridging role's own binding (SubtreeArchivedWorkers
// already covers that): ending only that one binding leaves the child
// orchestrator's own row with no parent link, so it reappears as a new
// top-level root on the next rail rebuild (the bug this fixes; Ctrl+D already
// cascades correctly for a LIVE bridging row via BridgeChildOrchID — this
// closes the same gap for an ARCHIVED one, which Ctrl+D never sees since it
// only fires on the currently-selected row). Empty when orchID is unknown or
// has no hidden descendant bridges.
func (m Model) SubtreeArchivedBridges(orchID int64) []int64 {
	bridge := m.bridgeIndex()
	var out []int64
	for _, o := range m.BridgeSubtree(orchID) {
		for i := range o.Roles {
			r := &o.Roles[i]
			if r.Kind != db.HeraKindWorker || !r.Archived {
				continue
			}
			if c := bridge[bridgeTaskID(r)]; c != nil && c.ID != o.ID {
				out = append(out, c.ID)
			}
		}
	}
	return out
}
