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
// coordinator's hidden WORKERS only. Empty when orchID is unknown or has no
// hidden descendant workers. (Nuked roles never appear in the Model, so this only
// ever returns Tier-1 hidden roles.)
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
