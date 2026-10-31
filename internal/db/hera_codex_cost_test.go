package db

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestListCostSweepBindings(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	o2 := mkOrch(t, d, "o2")
	live := mkBinding(t, d, mkRole(t, d, o.ID, "live", HeraKindWorker).ID, "t-live", "/w/live")
	recent := mkBinding(t, d, mkRole(t, d, o.ID, "recent", HeraKindWorker).ID, "t-recent", "/w/recent")
	old := mkBinding(t, d, mkRole(t, d, o.ID, "old", HeraKindWorker).ID, "t-old", "/w/old")
	dual := mkBinding(t, d, mkRole(t, d, o.ID, "dual-a", HeraKindWorker).ID, "t-dual", "/w/dual")
	mkBinding(t, d, mkRole(t, d, o2.ID, "dual-b", HeraKindCoordinator).ID, "t-dual", "/w/dual")

	testutil.NoError(t, d.EndHeraBinding(recent.ID, "test"))
	testutil.NoError(t, d.EndHeraBinding(old.ID, "test"))
	// Backdate "old" well outside the grace window.
	_, err := d.conn.Exec(`UPDATE hera_bindings SET ended_at=? WHERE id=?`,
		formatTime(time.Now().Add(-2*time.Hour)), old.ID)
	testutil.NoError(t, err)

	got, err := d.ListCostSweepBindings(time.Now().Add(-15 * time.Minute))
	testutil.NoError(t, err)
	byID := map[int64]CostSweepBinding{}
	for _, b := range got {
		byID[b.ID] = b
	}
	_, hasOld := byID[old.ID]
	testutil.False(t, hasOld)
	testutil.Equal(t, byID[live.ID].TaskLiveBindings, 1)
	testutil.Equal(t, byID[live.ID].WorktreePath, "/w/live")
	testutil.True(t, byID[live.ID].EndedAt == nil)
	testutil.True(t, byID[recent.ID].EndedAt != nil)
	testutil.Equal(t, byID[recent.ID].TaskLiveBindings, 0)
	testutil.Equal(t, byID[dual.ID].TaskLiveBindings, 2)
}

func TestAccrueCodexCalls(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	b := mkBinding(t, d, mkRole(t, d, o.ID, "w", HeraKindWorker).ID, "t", "/w")

	t.Run("adds tokens and cost and stores cursor", func(t *testing.T) {
		delta := HeraBindingCostTotals{TokensInput: 10, TokensCacheWrite5m: 3, TokensCacheRead: 90, TokensOutput: 5, CostUSDAccrued: 1.25}
		testutil.NoError(t, d.AccrueCodexCalls(b.ID, delta, CodexCostCursor{RolloutPath: "/r1", Offset: 100, LastTotal: 105, Model: "gpt"}))
		testutil.NoError(t, d.AccrueCodexCalls(b.ID, delta, CodexCostCursor{RolloutPath: "/r1", Offset: 200, LastTotal: 210, Model: "gpt"}))
		got, err := d.GetHeraBindingCostTotals(b.ID)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, *got, HeraBindingCostTotals{TokensInput: 20, TokensCacheWrite5m: 6, TokensCacheRead: 180, TokensOutput: 10, CostUSDAccrued: 2.5})
		curs, err := d.CodexCostCursors(b.ID)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, curs, map[string]CodexCostCursor{"/r1": {RolloutPath: "/r1", Offset: 200, LastTotal: 210, Model: "gpt"}})
	})

	t.Run("unknown binding is ErrHeraNotFound and writes no cursor", func(t *testing.T) {
		err := d.AccrueCodexCalls(99999, HeraBindingCostTotals{TokensInput: 1}, CodexCostCursor{RolloutPath: "/r"})
		testutil.True(t, errors.Is(err, ErrHeraNotFound))
		curs, err := d.CodexCostCursors(99999)
		testutil.NoError(t, err)
		testutil.Equal(t, len(curs), 0)
	})

	t.Run("cursors cascade on binding delete", func(t *testing.T) {
		_, err := d.conn.Exec(`DELETE FROM hera_bindings WHERE id=?`, b.ID)
		testutil.NoError(t, err)
		curs, err := d.CodexCostCursors(b.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(curs), 0)
	})
}
