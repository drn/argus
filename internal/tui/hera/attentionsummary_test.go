package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
)

// niSet builds a needs-input membership set from task ids.
func niSet(ids ...string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestUnmanagedNeedsInputCount_CountsTasksAbsentFromModel(t *testing.T) {
	m := heramodel.Model{Active: []heramodel.OrchView{orchView(1, "R", "tc", wk("w", "tw"))}}
	// tc + tw are model-known; tx and ty are not.
	got := m.UnmanagedNeedsInputCount(niSet("tc", "tw", "tx", "ty"))
	testutil.Equal(t, got, 2)
}

func TestUnmanagedNeedsInputCount_ExcludesManagedFreelanceAndFolded(t *testing.T) {
	// A coordinator (tc), a managed worker (tw) — managed regardless of whether the
	// rail folds its row, since the exclusion is over the MODEL not visible rows —
	// and a Hera freelance-role (tf). All three needing input must count as zero.
	m := heramodel.Model{
		Active: []heramodel.OrchView{orchView(1, "R", "tc", wk("w", "tw"))},
		Freelance: []heramodel.RoleView{
			{RoleID: 9, Name: "free", Kind: db.HeraKindFreelance, Live: true, TaskID: "tf"},
		},
	}
	got := m.UnmanagedNeedsInputCount(niSet("tc", "tw", "tf"))
	testutil.Equal(t, got, 0)
}

func TestUnmanagedNeedsInputCount_ExcludesViaBridgeTaskID(t *testing.T) {
	// A role whose live binding ended (TaskID empty) but whose latest-binding
	// structural key heramodel.BridgeTaskID is still set must be treated as managed.
	m := heramodel.Model{Active: []heramodel.OrchView{{
		ID:   1,
		Name: "R",
		Roles: []heramodel.RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
			{Name: "w", Kind: db.HeraKindWorker, BridgeTaskID: "tw-old"},
		},
	}}}
	got := m.UnmanagedNeedsInputCount(niSet("tc", "tw-old", "tx"))
	testutil.Equal(t, got, 1) // only tx
}

// TestUnmanagedNeedsInputCount_ExcludesArchivedRoleBoundTask is the BUG-005
// regression lock: a task bound to an ARCHIVED hera role (binding ended, role
// archived) still has Hera presence via its heramodel.BridgeTaskID, so it must NOT be
// counted — only a genuinely Hera-less task does.
func TestUnmanagedNeedsInputCount_ExcludesArchivedRoleBoundTask(t *testing.T) {
	m := heramodel.Model{Active: []heramodel.OrchView{{
		ID:   1,
		Name: "R",
		Roles: []heramodel.RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
			// Archived worker whose live binding ended; structural key survives.
			{Name: "old-wkr", Kind: db.HeraKindWorker, Archived: true, BridgeTaskID: "tarch"},
		},
	}}}
	// tc + tarch are model-known (managed/archived); only ty has no Hera presence.
	got := m.UnmanagedNeedsInputCount(niSet("tc", "tarch", "ty"))
	testutil.Equal(t, got, 1)
}

func TestUnmanagedNeedsInputCount_ZeroWhenAllKnownOrEmpty(t *testing.T) {
	m := heramodel.Model{Active: []heramodel.OrchView{orchView(1, "R", "tc", wk("w", "tw"))}}
	testutil.Equal(t, m.UnmanagedNeedsInputCount(niSet("tc", "tw")), 0)
	testutil.Equal(t, m.UnmanagedNeedsInputCount(nil), 0)
	testutil.Equal(t, heramodel.Model{}.UnmanagedNeedsInputCount(niSet("tx")), 1)
}

func TestUnmanagedNeedsInputCount_CountsAcrossPinnedAndArchivedExclusions(t *testing.T) {
	m := heramodel.Model{
		Pinned:   []heramodel.OrchView{orchView(1, "P", "tp", wk("pw", "tpw"))},
		Active:   []heramodel.OrchView{orchView(2, "A", "ta")},
		Archived: []heramodel.OrchView{orchView(3, "Z", "tz")},
	}
	// tp, tpw, ta, tz are all model-known across the three sections; only u1/u2 are not.
	got := m.UnmanagedNeedsInputCount(niSet("tp", "tpw", "ta", "tz", "u1", "u2"))
	testutil.Equal(t, got, 2)
}
